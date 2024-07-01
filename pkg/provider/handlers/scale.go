package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"

	"github.com/containerd/containerd"
	"github.com/containerd/containerd/namespaces"
	gocni "github.com/containerd/go-cni"

	"github.com/openfaas/faas-provider/types"
	"github.com/openfaas/faasd/pkg"
	faasd "github.com/openfaas/faasd/pkg"
	"github.com/openfaas/faasd/pkg/provider/catalog"
)

// MaxReplicas licensed for OpenFaaS CE is 5/5
const MaxReplicas uint64 = 5

type ctxstring string

const offloadKey ctxstring = "offload"

func MakeReplicaUpdateHandler(client *containerd.Client, cni gocni.CNI, secretMountPath string, c catalog.Catalog) func(w http.ResponseWriter, r *http.Request) {

	return func(w http.ResponseWriter, r *http.Request) {

		if r.Body == nil {
			http.Error(w, "expected a body", http.StatusBadRequest)
			return
		}

		defer r.Body.Close()

		body, _ := ioutil.ReadAll(r.Body)
		log.Printf("[Scale] request: %s\n", string(body))

		req := types.ScaleServiceRequest{}
		if err := json.Unmarshal(body, &req); err != nil {
			log.Printf("[Scale] error parsing input: %s\n", err)
			http.Error(w, err.Error(), http.StatusBadRequest)

			return
		}

		// namespace := req.Namespace
		// if namespace == "" {
		namespace := pkg.DefaultFunctionNamespace
		// }

		// Check if namespace exists, and it has the openfaas label
		valid, err := validNamespace(client.NamespaceService(), namespace)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		if !valid {
			http.Error(w, "namespace not valid", http.StatusBadRequest)
			return
		}

		name := req.ServiceName

		fn, fnExist := c.FunctionCatalog[name]
		if !fnExist {
			msg := fmt.Sprintf("service %s not found", name)
			log.Printf("[Scale] %s\n", msg)
			http.Error(w, msg, http.StatusNotFound)
			return
		}

		replicas := req.Replicas
		if req.Replicas > MaxReplicas {
			replicas = MaxReplicas
		} else if req.Replicas <= 0 {
			http.Error(w, "replicas cannot be set to 0 in OpenFaaS CE",
				http.StatusBadRequest)
			return
		}

		// no change or second hand scale up request.

		if fn.Replicas == replicas {
			log.Printf("Scale %s: stay replica %d\n", name, fn.Replicas)
			w.WriteHeader(http.StatusNoContent)
			return
		} else if isOffloadRequest(r) {
			// Currently the faasd can not scale up the number of container, so if it receive the scale up from other, just stay
			log.Printf("Scale %s: stay replica %d\n", name, fn.Replicas)
			w.WriteHeader(http.StatusNoContent)
		} else if fn.Replicas < replicas { //scale up
			log.Printf("Scale up %s: replica %d->%d\n", name, fn.Replicas, replicas)
			err := scaleUp(name, replicas, client, cni, secretMountPath, c)
			if err != nil {
				log.Printf("[Scale] %s\n", err)
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
		} else { // scale down
			log.Printf("Scale down %s: replica %d->%d\n", name, fn.Replicas, replicas)
			scaleDown(name, replicas, client, cni, c)
		}
	}
}

// TODO: maybe try to change it to context?
// func isOffloadRequestCtx(ctx context.Context) bool {
// 	offload := ctx.Value(offloadkey)
// 	fmt.Printf("Get the context from request: %s\n", offload)
// 	// If there are no values associated with the key, Get returns the empty string
// 	return offload == "1"
// }

// TODO: first use the naive solution, sequentially scale up
func scaleUp(functionName string, desiredReplicas uint64, client *containerd.Client, cni gocni.CNI, secretMountPath string, c catalog.Catalog) error {
	scaleUpCnt := desiredReplicas - c.FunctionCatalog[functionName].Replicas
	// try scale up the function from near to far

	for i := 0; i < len(*c.SortedP2PID) && scaleUpCnt > 0; i++ {
		p2pID := (*c.SortedP2PID)[i]
		availableFunctionsReplicas := c.NodeCatalog[p2pID].AvailableFunctionsReplicas[functionName]
		// first deploy as there is no instance yet
		if availableFunctionsReplicas == 0 {
			// is this necssary? will the trigger point has no function?
			err := deployFunctionByP2PID(faasd.DefaultFunctionNamespace, functionName, client, cni, secretMountPath, p2pID, c)
			if err != nil {
				log.Printf("make new function error: %v\n", err)
				return err
			}
			go func() {
				fn, err := waitDeployReadyAndReport(client, c.NodeCatalog[p2pID].FaasClient, functionName)
				if err != nil {
					log.Printf("[Deploy] error deploying %s, error: %s\n", functionName, err)
					return
				}
				// only report add for itself
				if p2pID == catalog.GetSelfCatalogKey() {
					c.AddAvailableFunctions(fn)
				}
			}()
			// deploy success mean scale up one instance
			scaleUpCnt--
			availableFunctionsReplicas += 1
		}
		// try to scale up to desire replicas (all-or-nothing)
		if scaleUpCnt > 0 && p2pID != catalog.GetSelfCatalogKey() {
			// To memorize the next request do not do the scale decision again to prevent recursive
			ctx := context.WithValue(context.Background(), offloadKey, "1")
			scaleErr := c.NodeCatalog[p2pID].FaasClient.Client.ScaleFunction(ctx, functionName, faasd.DefaultFunctionNamespace, scaleUpCnt)
			// no error mean scale success
			if scaleErr == nil {
				scaleUpCnt = 0
			}
		}
	}
	return nil
}

func scaleDown(functionName string, desiredReplicas uint64, client *containerd.Client, cni gocni.CNI, c catalog.Catalog) {
	scaleDownCnt := c.FunctionCatalog[functionName].Replicas - desiredReplicas

	// remove the function from the far instance
	for i := 0; i < len(*c.SortedP2PID) && scaleDownCnt > 0; i++ {
		p2pID := (*c.SortedP2PID)[i]
		availableFunctionsReplicas := c.NodeCatalog[p2pID].AvailableFunctionsReplicas[functionName]
		if availableFunctionsReplicas > 0 {
			// the replica is more than the scale down count
			if availableFunctionsReplicas > scaleDownCnt {
				if p2pID == catalog.GetSelfCatalogKey() {
					// TODO: currently the own thing can do is to delete, change to real scale down in future
					DeleteFunction(client, cni, functionName, faasd.DefaultFunctionNamespace)
					// use the update instead of delete
					fn := *c.FunctionCatalog[functionName]
					fn.AvailableReplicas, fn.Replicas = 0, 0
					c.UpdateAvailableFunctions(fn)
				} else {
					// To memorize the next request do not do the scale decision again to prevent recursive
					ctx := context.WithValue(context.Background(), offloadKey, "1")
					c.NodeCatalog[p2pID].FaasClient.Client.ScaleFunction(ctx, functionName, faasd.DefaultFunctionNamespace, availableFunctionsReplicas-scaleDownCnt)
				}
				scaleDownCnt = 0
			} else {
				if p2pID == catalog.GetSelfCatalogKey() {
					DeleteFunction(client, cni, functionName, faasd.DefaultFunctionNamespace)
					// c.DeleteAvailableFunctions(functionName)
					fn := *c.FunctionCatalog[functionName]
					fn.AvailableReplicas, fn.Replicas = 0, 0
					c.UpdateAvailableFunctions(fn)
				} else {
					c.NodeCatalog[p2pID].FaasClient.Client.DeleteFunction(context.Background(), functionName, faasd.DefaultFunctionNamespace)
				}
				scaleDownCnt -= availableFunctionsReplicas
			}
		}
	}

}

func deployFunctionByP2PID(functionNamespace string, functionName string, client *containerd.Client, cni gocni.CNI, secretMountPath string, targetP2PID string, c catalog.Catalog) error {
	targetFunction, exist := c.FunctionCatalog[functionName]
	if !exist {
		err := fmt.Errorf("no endpoints available for: %s", functionName)
		return err
	}
	deployment := types.FunctionDeployment{
		Service:                targetFunction.Name,
		Image:                  targetFunction.Image,
		Namespace:              targetFunction.Namespace,
		EnvProcess:             targetFunction.EnvProcess,
		EnvVars:                targetFunction.EnvVars,
		Constraints:            targetFunction.Constraints,
		Secrets:                targetFunction.Secrets,
		Labels:                 targetFunction.Labels,
		Annotations:            targetFunction.Annotations,
		Limits:                 targetFunction.Limits,
		Requests:               targetFunction.Requests,
		ReadOnlyRootFilesystem: targetFunction.ReadOnlyRootFilesystem,
	}
	if targetP2PID == catalog.GetSelfCatalogKey() {
		ctx := namespaces.WithNamespace(context.Background(), functionNamespace)
		namespaceSecretMountPath := getNamespaceSecretMountPath(secretMountPath, faasd.DefaultFunctionNamespace)
		err := validateSecrets(namespaceSecretMountPath, targetFunction.Secrets)
		if err != nil {
			log.Printf("error getting %s secret, error: %s\n", functionName, err)
			return err
		}
		deployErr := deploy(ctx, deployment, client, cni, namespaceSecretMountPath, false)
		if deployErr != nil {
			log.Printf("error deploying %s, error: %s\n", functionName, deployErr)
			return err
		}
		// update the catalog until the function is ready
		// go func() {
		// 	fn, err := waitDeployReadyAndReport(client, c.NodeCatalog[catalog.GetSelfCatalogKey()].FaasClient, functionName)
		// 	if err != nil {
		// 		log.Printf("[Deploy] error deploying %s, error: %s\n", functionName, err)
		// 		return
		// 	}
		// 	c.AddAvailableFunctions(fn)
		// }()
	} else {
		_, err := c.NodeCatalog[targetP2PID].FaasClient.Client.Deploy(context.Background(), deployment)
		if err != nil {
			return err
		}
	}

	return nil
}
