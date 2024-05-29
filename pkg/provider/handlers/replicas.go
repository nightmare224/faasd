package handlers

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/containerd/containerd"
	"github.com/gorilla/mux"
	faasd "github.com/openfaas/faasd/pkg"
	"github.com/openfaas/faasd/pkg/provider/catalog"
)

func MakeReplicaReaderHandler(client *containerd.Client, c catalog.Catalog) func(w http.ResponseWriter, r *http.Request) {

	return func(w http.ResponseWriter, r *http.Request) {
		vars := mux.Vars(r)
		functionName := vars["name"]
		// lookupNamespace := getRequestNamespace(readNamespaceFromQuery(r))

		// Check if namespace exists, and it has the openfaas label
		// valid, err := validNamespace(client.NamespaceService(), lookupNamespace)
		// if err != nil {
		// 	http.Error(w, err.Error(), http.StatusBadRequest)
		// 	return
		// }

		// if !valid {
		// 	http.Error(w, "namespace not valid", http.StatusBadRequest)
		// 	return
		// }

		// if f, err := GetFunction(client, functionName, lookupNamespace); err == nil {
		// 	found := types.FunctionStatus{
		// 		Name:              functionName,
		// 		Image:             f.image,
		// 		AvailableReplicas: uint64(f.replicas),
		// 		Replicas:          uint64(f.replicas),
		// 		Namespace:         f.namespace,
		// 		Labels:            &f.labels,
		// 		Annotations:       &f.annotations,
		// 		Secrets:           f.secrets,
		// 		EnvVars:           f.envVars,
		// 		EnvProcess:        f.envProcess,
		// 		CreatedAt:         f.createdAt,
		// 	}

		// 	functionBytes, _ := json.Marshal(found)
		// 	w.Header().Set("Content-Type", "application/json")
		// 	w.WriteHeader(http.StatusOK)
		// 	w.Write(functionBytes)
		// } else {
		// 	w.WriteHeader(http.StatusNotFound)
		// }
		// var targetFunction types.FunctionStatus
		// for _, node := range c {
		// for _, fn := range node.AvailableFunctions {
		// 	// fmt.Printf("target function %s, available func %s.\n", functionName, fn.Name)
		// 	// use or without namespace
		// 	if functionName == fn.Name || functionName == fmt.Sprintf("%s.%s", fn.Name, fn.Namespace) {
		// 		targetFunction = fn
		// 		found = true
		// 		break
		// 	}
		// }
		// }
		fname := functionName
		if strings.Contains(functionName, ".") {
			fname = strings.TrimSuffix(functionName, "."+faasd.DefaultFunctionNamespace)
		}
		if fn, err := c.GetAvailableFunction(fname); err == nil {
			//TODO: the available replicas here do not show the replica in the host, but show the
			// overall replica, because if show 0 the gateway would consider it not ready
			fn.AvailableReplicas = max(fn.Replicas, fn.AvailableReplicas)
			functionBytes, _ := json.Marshal(fn)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			w.Write(functionBytes)
		} else {
			w.WriteHeader(http.StatusNotFound)
		}

	}
}
