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

func MakeReplicaUpdateHandler(client *containerd.Client, cni gocni.CNI, secretMountPath string, faasP2PMappingList catalog.FaasP2PMappingList, c catalog.Catalog) func(w http.ResponseWriter, r *http.Request) {

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
		} else if req.Replicas == 0 {
			log.Printf("replicas cannot be set to 0 (currently disabled), set to 1 replica\n")
			replicas = 1
		}

		// no change or second hand scale up request.
		// Currently the faasd can not scale up the number of container, so if it receive the scale up from other, just stay
		if fn.Replicas == replicas || isOffloadRequest(r) {
			log.Printf("Scale %s: stay replica %d\n", name, fn.Replicas)
			w.WriteHeader(http.StatusNoContent)
			return
		} else if fn.Replicas < replicas { //scale up
			log.Printf("Scale up %s: replica %d->%d\n", name, fn.Replicas, replicas)
			err := scaleUp(name, replicas, client, cni, secretMountPath, faasP2PMappingList, c)
			if err != nil {
				log.Printf("[Scale] %s\n", err)
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
		} else { // scale down
			log.Printf("Scale down %s: replica %d->%d\n", name, fn.Replicas, replicas)
			scaleDown(name, replicas, client, cni, faasP2PMappingList, c)
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
func scaleUp(functionName string, desiredReplicas uint64, client *containerd.Client, cni gocni.CNI, secretMountPath string, faasP2PMappingList catalog.FaasP2PMappingList, c catalog.Catalog) error {
	scaleUpCnt := desiredReplicas - c.FunctionCatalog[functionName].Replicas
	// try scale up the function from near to far
	fn := c.FunctionCatalog[functionName]
	deployment := types.FunctionDeployment{
		Service:                fn.Name,
		Image:                  fn.Image,
		Namespace:              fn.Namespace,
		EnvProcess:             fn.EnvProcess,
		EnvVars:                fn.EnvVars,
		Constraints:            fn.Constraints,
		Secrets:                fn.Secrets,
		Labels:                 fn.Labels,
		Annotations:            fn.Annotations,
		Limits:                 fn.Limits,
		Requests:               fn.Requests,
		ReadOnlyRootFilesystem: fn.ReadOnlyRootFilesystem,
	}
	namespaceSecretMountPath := getNamespaceSecretMountPath(secretMountPath, faasd.DefaultFunctionNamespace)
	err := validateSecrets(namespaceSecretMountPath, fn.Secrets)
	if err != nil {
		log.Printf("error getting %s secret, error: %s\n", functionName, err)
		return err
	}
	for i := 0; i < len(faasP2PMappingList) && scaleUpCnt > 0; i++ {
		p2pID := faasP2PMappingList[i].P2PID
		availableFunctionsReplicas := c.NodeCatalog[p2pID].AvailableFunctionsReplicas[functionName]
		// first deploy as there is no instance yet
		if availableFunctionsReplicas == 0 {
			if p2pID == c.GetSelfCatalogKey() {
				ctx := namespaces.WithNamespace(context.Background(), faasd.DefaultFunctionNamespace)
				deployErr := deploy(ctx, deployment, client, cni, namespaceSecretMountPath, false)
				if deployErr != nil {
					log.Printf("error deploying %s, error: %s\n", functionName, deployErr)
					return err
				}
				fn, err := waitDeployReadyAndReport(client, cni, functionName)
				if err != nil {
					log.Printf("error waiting %s, error: %s\n", functionName, err)
					return err
				}
				c.AddAvailableFunctions(fn)
			} else {
				_, err := faasP2PMappingList[i].FaasClient.Deploy(context.Background(), deployment)
				if err != nil {
					return err
				}
			}
			// deploy success mean scale up one instance
			scaleUpCnt--
		}
		// try to scale up to desire replicas (all-or-nothing)
		if scaleUpCnt > 0 && p2pID != c.GetSelfCatalogKey() {
			// To memorize the next request do not do the scale decision again to prevent recursive
			ctx := context.WithValue(context.Background(), offloadKey, "1")
			scaleErr := faasP2PMappingList[i].FaasClient.ScaleFunction(ctx, functionName, faasd.DefaultFunctionNamespace, scaleUpCnt)
			// no error mean scale success
			if scaleErr == nil {
				scaleUpCnt = 0
			}
		}
	}
	return nil
}

func scaleDown(functionName string, desiredReplicas uint64, client *containerd.Client, cni gocni.CNI, faasP2PMappingList catalog.FaasP2PMappingList, c catalog.Catalog) {
	scaleDownCnt := c.FunctionCatalog[functionName].Replicas - desiredReplicas

	// remove the function from the far instance
	for i := len(faasP2PMappingList) - 1; i >= 0 && scaleDownCnt > 0; i++ {
		p2pID := faasP2PMappingList[i].P2PID
		availableFunctionsReplicas := c.NodeCatalog[p2pID].AvailableFunctionsReplicas[functionName]
		if availableFunctionsReplicas > 0 {
			// the replica is more than the scale down count
			if availableFunctionsReplicas > scaleDownCnt {
				if p2pID == c.GetSelfCatalogKey() {
					// TODO: currently the own thing can do is to delete, change to real scale down in future
					DeleteFunction(client, cni, functionName, faasd.DefaultFunctionNamespace)
					c.DeleteAvailableFunctions(functionName)
				} else {
					// To memorize the next request do not do the scale decision again to prevent recursive
					ctx := context.WithValue(context.Background(), offloadKey, "1")
					faasP2PMappingList[i].FaasClient.ScaleFunction(ctx, functionName, faasd.DefaultFunctionNamespace, availableFunctionsReplicas-scaleDownCnt)
				}
				scaleDownCnt = 0
			} else {
				if p2pID == c.GetSelfCatalogKey() {
					DeleteFunction(client, cni, functionName, faasd.DefaultFunctionNamespace)
					c.DeleteAvailableFunctions(functionName)
				} else {
					faasP2PMappingList[i].FaasClient.DeleteFunction(context.Background(), functionName, faasd.DefaultFunctionNamespace)
				}
				scaleDownCnt -= availableFunctionsReplicas
			}
		}
	}

}
