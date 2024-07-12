package handlers

import (
	"encoding/json"
	"io/ioutil"
	"log"
	"net/http"

	"github.com/containerd/containerd"
	gocni "github.com/containerd/go-cni"

	"github.com/openfaas/faas-provider/types"
	"github.com/openfaas/faasd/pkg"
	"github.com/openfaas/faasd/pkg/provider/catalog"
)

func MakeDeleteHandler(client *containerd.Client, cni gocni.CNI, c catalog.Catalog) func(w http.ResponseWriter, r *http.Request) {

	return func(w http.ResponseWriter, r *http.Request) {

		if r.Body == nil {
			http.Error(w, "expected a body", http.StatusBadRequest)
			return
		}

		defer r.Body.Close()

		body, _ := ioutil.ReadAll(r.Body)
		log.Printf("[Delete] request: %s\n", string(body))

		req := types.DeleteFunctionRequest{}
		err := json.Unmarshal(body, &req)
		if err != nil {
			log.Printf("[Delete] error parsing input: %s\n", err)
			http.Error(w, err.Error(), http.StatusBadRequest)

			return
		}

		// namespace moved from the querystring into the body
		namespace := req.Namespace
		if namespace == "" {
			namespace = pkg.DefaultFunctionNamespace
		}

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

		name := req.FunctionName

		// update the catalog (report before delete)
		c.DeleteAvailableFunctions(name)

		delErr := DeleteFunction(client, cni, name, namespace)
		if delErr != nil {
			http.Error(w, delErr.Error(), http.StatusBadRequest)
			return
		}

		log.Printf("[Delete] deleted %s\n", name)
	}
}
