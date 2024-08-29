package handlers

import (
	"encoding/json"
	"net/http"

	"github.com/containerd/containerd"
	"github.com/openfaas/faasd/pkg/provider/catalog"
)

func MakeReadHandler(client *containerd.Client, c catalog.Catalog) func(w http.ResponseWriter, r *http.Request) {

	return func(w http.ResponseWriter, r *http.Request) {

		infoLevel := catalog.ClusterLevel
		res := c.ListAvailableFunctions(infoLevel)
		body, _ := json.Marshal(res)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write(body)
	}
}
