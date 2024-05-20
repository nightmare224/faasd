package handlers

import (
	"encoding/json"
	"net/http"

	"github.com/containerd/containerd"
	"github.com/openfaas/faasd/pkg/provider/catalog"
)

func MakeReadHandler(client *containerd.Client, c catalog.Catalog) func(w http.ResponseWriter, r *http.Request) {

	return func(w http.ResponseWriter, r *http.Request) {

		// lookupNamespace := getRequestNamespace(readNamespaceFromQuery(r))
		// // Check if namespace exists, and it has the openfaas label
		// valid, err := validNamespace(client.NamespaceService(), lookupNamespace)
		// if err != nil {
		// 	http.Error(w, err.Error(), http.StatusBadRequest)
		// 	return
		// }

		// if !valid {
		// 	http.Error(w, "namespace not valid", http.StatusBadRequest)
		// 	return
		// }

		// res := []types.FunctionStatus{}
		// fns, err := ListFunctions(client, lookupNamespace)
		// if err != nil {
		// 	log.Printf("[Read] error listing functions. Error: %s\n", err)
		// 	http.Error(w, err.Error(), http.StatusBadRequest)
		// 	return
		// }

		// for _, fn := range fns {
		// 	annotations := &fn.annotations
		// 	labels := &fn.labels
		// 	memory := resource.NewQuantity(fn.memoryLimit, resource.BinarySI)
		// 	status := types.FunctionStatus{
		// 		Name:        fn.name,
		// 		Image:       fn.image,
		// 		Replicas:    uint64(fn.replicas),
		// 		Namespace:   fn.namespace,
		// 		Labels:      labels,
		// 		Annotations: annotations,
		// 		Secrets:     fn.secrets,
		// 		EnvVars:     fn.envVars,
		// 		EnvProcess:  fn.envProcess,
		// 		CreatedAt:   fn.createdAt,
		// 	}

		// 	// Do not remove below memory check for 0
		// 	// Memory limit should not be included in status until set explicitly
		// 	limit := &types.FunctionResources{Memory: memory.String()}
		// 	if limit.Memory != "0" {
		// 		status.Limits = limit
		// 	}

		// 	res = append(res, status)
		// }

		// // do not use the record in catalog as it need detail information of function
		// // if it is from internal client, then don't run this one, as it hit the same API
		// // res, err = includeExternalFunction(res, faasP2PMappingList)
		// if err != nil {
		// 	http.Error(w, err.Error(), http.StatusBadRequest)
		// 	return
		// }
		infoLevel := catalog.ClusterLevel
		res := c.ListAvailableFunctions(infoLevel)
		body, _ := json.Marshal(res)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write(body)
	}
}

// func includeExternalFunction(functionStatus []types.FunctionStatus, faasP2PMappingList []catalog.FaasP2PMapping) ([]types.FunctionStatus, error) {

// 	functionnameSet := make(map[string]struct{})
// 	for _, fn := range functionStatus {
// 		functionnameSet[fn.Name] = struct{}{}
// 	}

// 	for _, faasP2PMapping := range faasP2PMappingList {
// 		fns, err := faasP2PMapping.FaasClient.GetFunctions(context.Background(), "openfaas-fn")
// 		if err != nil {
// 			log.Printf("[Read] error listing external functions. Error: %s\n", err)
// 			return nil, err
// 		}
// 		for _, fn := range fns {
// 			if _, exist := functionnameSet[fn.Name]; !exist {
// 				functionStatus = append(functionStatus, fn)
// 				functionnameSet[fn.Name] = struct{}{}
// 			}
// 		}
// 	}

// 	return functionStatus, nil

// }
