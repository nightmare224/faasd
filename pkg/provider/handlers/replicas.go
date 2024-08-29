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
