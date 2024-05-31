package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"

	"github.com/containerd/containerd"
	gocni "github.com/containerd/go-cni"

	"github.com/openfaas/faas-provider/types"
	"github.com/openfaas/faasd/pkg"
	faasd "github.com/openfaas/faasd/pkg"
	"github.com/openfaas/faasd/pkg/provider/catalog"
)

// MaxReplicas licensed for OpenFaaS CE is 5/5
const MaxReplicas uint64 = 5

func MakeReplicaUpdateHandler(client *containerd.Client, cni gocni.CNI, faasP2PMappingList catalog.FaasP2PMappingList, c catalog.Catalog) func(w http.ResponseWriter, r *http.Request) {

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

		// scale up
		if fn.Replicas < replicas {
			// scaleUp()
		} else if fn.Replicas > replicas { // scale down
			log.Printf("[Scale] %s replica %d->%d\n", name, fn.Replicas, replicas)
			scaleDown(name, replicas, client, cni, faasP2PMappingList, c)

		} else {
			log.Printf("[Scale] %s stay replica %d\n", name, fn.Replicas)
			return
		}

		// if _, err := GetFunction(client, name, namespace); err != nil {
		// 	msg := fmt.Sprintf("service %s not found", name)
		// 	log.Printf("[Scale] %s\n", msg)
		// 	http.Error(w, msg, http.StatusNotFound)
		// 	return
		// }

		// ctx := namespaces.WithNamespace(context.Background(), namespace)

		// ctr, ctrErr := client.LoadContainer(ctx, name)
		// if ctrErr != nil {
		// 	msg := fmt.Sprintf("cannot load service %s, error: %s", name, ctrErr)
		// 	log.Printf("[Scale] %s\n", msg)
		// 	http.Error(w, msg, http.StatusNotFound)
		// 	return
		// }

		// var taskExists bool
		// var taskStatus *containerd.Status

		// task, taskErr := ctr.Task(ctx, nil)
		// if taskErr != nil {
		// 	msg := fmt.Sprintf("cannot load task for service %s, error: %s", name, taskErr)
		// 	log.Printf("[Scale] %s\n", msg)
		// 	taskExists = false
		// } else {
		// 	taskExists = true
		// 	status, statusErr := task.Status(ctx)
		// 	if statusErr != nil {
		// 		msg := fmt.Sprintf("cannot load task status for %s, error: %s", name, statusErr)
		// 		log.Printf("[Scale] %s\n", msg)
		// 		http.Error(w, msg, http.StatusInternalServerError)
		// 		return
		// 	} else {
		// 		taskStatus = &status
		// 	}
		// }

		// createNewTask := false

		// // Scale to zero
		// // if req.Replicas == 0 {
		// // 	// If a task is running, pause it
		// // 	if taskExists && taskStatus.Status == containerd.Running {
		// // 		if pauseErr := task.Pause(ctx); pauseErr != nil {
		// // 			wrappedPauseErr := fmt.Errorf("error pausing task %s, error: %s", name, pauseErr)
		// // 			log.Printf("[Scale] %s\n", wrappedPauseErr.Error())
		// // 			http.Error(w, wrappedPauseErr.Error(), http.StatusNotFound)
		// // 			return
		// // 		}
		// // 	}
		// // }
		// if req.Replicas == 0 {
		// 	http.Error(w, "replicas cannot be set to 0 (currently disabled)",
		// 		http.StatusBadRequest)
		// 	return
		// }

		// if taskExists {
		// 	if taskStatus != nil {
		// 		if taskStatus.Status == containerd.Paused {
		// 			if resumeErr := task.Resume(ctx); resumeErr != nil {
		// 				log.Printf("[Scale] error resuming task %s, error: %s\n", name, resumeErr)
		// 				http.Error(w, resumeErr.Error(), http.StatusBadRequest)
		// 				return
		// 			}
		// 		} else if taskStatus.Status == containerd.Stopped {
		// 			// Stopped tasks cannot be restarted, must be removed, and created again
		// 			if _, delErr := task.Delete(ctx); delErr != nil {
		// 				log.Printf("[Scale] error deleting stopped task %s, error: %s\n", name, delErr)
		// 				http.Error(w, delErr.Error(), http.StatusBadRequest)
		// 				return
		// 			}
		// 			createNewTask = true
		// 		}
		// 	}
		// } else {
		// 	createNewTask = true
		// }

		// if createNewTask {
		// 	deployErr := createTask(ctx, ctr, cni)
		// 	if deployErr != nil {
		// 		log.Printf("[Scale] error deploying %s, error: %s\n", name, deployErr)
		// 		http.Error(w, deployErr.Error(), http.StatusBadRequest)
		// 		return
		// 	}
		// }

		// scale up
		// replicas := req.Replicas
		// if req.Replicas >= MaxReplicas {
		// 	replicas = MaxReplicas
		// }
		// if replicas > c.FunctionCatalog[name].Replicas {
		// 	scaleUp(name, replicas, c)
		// }
	}
}
func scaleDown(functionName string, desiredReplicas uint64, client *containerd.Client, cni gocni.CNI, faasP2PMappingList catalog.FaasP2PMappingList, c catalog.Catalog) {
	scaleDownCnt := c.FunctionCatalog[functionName].Replicas - desiredReplicas

	// remove the function from the far instance
	for i := len(faasP2PMappingList) - 1; i > 0 && scaleDownCnt > 0; i++ {
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
					faasP2PMappingList[i].FaasClient.ScaleFunction(context.Background(), functionName, faasd.DefaultFunctionNamespace, availableFunctionsReplicas-scaleDownCnt)
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

// func scaleUp(functionName string, functionReplicas uint64, c catalog.Catalog) {
// 	for p2pID, node := range c.NodeCatalog {
// 		if _, exist := node.AvailableFunctionsReplicas[functionName]; !exist {

// 		}
// 	}
// }
