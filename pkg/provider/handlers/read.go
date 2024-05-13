package handlers

import (
	"context"
	"encoding/json"
	"log"
	"net/http"

	"k8s.io/apimachinery/pkg/api/resource"

	"github.com/containerd/containerd"
	"github.com/openfaas/faas-provider/types"
	"github.com/openfaas/go-sdk"
)

func MakeReadHandler(client *containerd.Client, externalClients []*sdk.Client) func(w http.ResponseWriter, r *http.Request) {

	return func(w http.ResponseWriter, r *http.Request) {

		lookupNamespace := getRequestNamespace(readNamespaceFromQuery(r))
		// Check if namespace exists, and it has the openfaas label
		valid, err := validNamespace(client.NamespaceService(), lookupNamespace)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		if !valid {
			http.Error(w, "namespace not valid", http.StatusBadRequest)
			return
		}

		res := []types.FunctionStatus{}
		fns, err := ListFunctions(client, lookupNamespace)
		if err != nil {
			log.Printf("[Read] error listing functions. Error: %s\n", err)
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		for _, fn := range fns {
			annotations := &fn.annotations
			labels := &fn.labels
			memory := resource.NewQuantity(fn.memoryLimit, resource.BinarySI)
			status := types.FunctionStatus{
				Name:        fn.name,
				Image:       fn.image,
				Replicas:    uint64(fn.replicas),
				Namespace:   fn.namespace,
				Labels:      labels,
				Annotations: annotations,
				Secrets:     fn.secrets,
				EnvVars:     fn.envVars,
				EnvProcess:  fn.envProcess,
				CreatedAt:   fn.createdAt,
			}

			// Do not remove below memory check for 0
			// Memory limit should not be included in status until set explicitly
			limit := &types.FunctionResources{Memory: memory.String()}
			if limit.Memory != "0" {
				status.Limits = limit
			}

			res = append(res, status)
		}

		// do not use the record in catalog as it need detail information of function
		res, err = includeExternalFunction(res, externalClients)
		if err != nil {
			log.Printf("[Read] error listing functions. Error: %s\n", err)
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		body, _ := json.Marshal(res)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write(body)
	}
}
func includeExternalFunction(functionStatus []types.FunctionStatus, externalClients []*sdk.Client) ([]types.FunctionStatus, error) {

	functionnameSet := make(map[string]struct{})
	for _, fn := range functionStatus {
		functionnameSet[fn.Name] = struct{}{}
	}

	for _, client := range externalClients {
		fns, err := client.GetFunctions(context.Background(), "openfaas-fn")
		if err != nil {
			log.Printf("[Read] error listing functions. Error: %s\n", err)
			return nil, err
		}
		for _, fn := range fns {
			if _, exist := functionnameSet[fn.Name]; !exist {
				functionStatus = append(functionStatus, fn)
				functionnameSet[fn.Name] = struct{}{}
			}
		}
	}

	return functionStatus, nil

}
