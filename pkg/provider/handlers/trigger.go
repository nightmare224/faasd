package handlers

import (
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync/atomic"
	"time"

	"github.com/gorilla/mux"
	weightedrand "github.com/mroth/weightedrand/v2"
	fhttputil "github.com/openfaas/faas-provider/httputil"
	"github.com/openfaas/faas-provider/proxy"
	"github.com/openfaas/faas-provider/types"
	faasd "github.com/openfaas/faasd/pkg"
	"github.com/openfaas/faasd/pkg/provider/catalog"
)

func MakeTriggerHandler(config types.FaaSConfig, resolver proxy.BaseURLResolver, c catalog.Catalog) http.HandlerFunc {

	// enableOffload := true
	return func(w http.ResponseWriter, r *http.Request) {
		// fmt.Printf("Receive the trigger!\n")
		// if enableOffload && !isOffloadRequest(r) {
		// this should be trigger the target faas client
		vars := mux.Vars(r)
		functionName := vars["name"]
		if strings.Contains(vars["name"], ".") {
			functionName = strings.TrimSuffix(vars["name"], "."+faasd.DefaultFunctionNamespace)
		}
		targetFunction, exist := c.FunctionCatalog[functionName]
		if !exist {
			err := fmt.Errorf("no endpoints available for: %s", functionName)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		targetP2PID := catalog.GetSelfCatalogKey()
		var err error
		if !isOffloadRequest(r) {
			targetP2PID, err = findSuitableNode(functionName, c)
			if err != nil {
				fmt.Printf("Unable to trigger function: %v\n", err.Error())
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
		}
		// no deploy required, just trigger
		if _, exist := c.NodeCatalog[targetP2PID].AvailableFunctionsReplicas[functionName]; !exist {
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
			c.NodeCatalog[targetP2PID].FaasClient.Client.Deploy(context.Background(), deployment)
			// TODO: wait unitl the function ready
		}
		// trigger the function here
		// if non local than change the resolver
		invokeResolver := resolver
		if targetP2PID != catalog.GetSelfCatalogKey() {
			invokeResolver = &c.NodeCatalog[targetP2PID].FaasClient
		}
		markAsOffloadRequest(r)
		offloadRequest(w, r, config, invokeResolver, c.NodeCatalog[targetP2PID].FunctionExecutionTime)
	}
}
func markAsOffloadRequest(r *http.Request) {
	r.URL.RawQuery = "offload=1"
}
func isOffloadRequest(r *http.Request) bool {
	offload := r.URL.Query().Get("offload")

	// If there are no values associated with the key, Get returns the empty string
	return offload == "1"
}

// return the select p2pid to execution function based on last weighted exec time
func weightExecTimeScheduler(functionName string, NodeCatalog map[string]*catalog.Node) (string, error) {

	// var choices []*weightedrand.Chooser[T, W]
	var execTimeProd int64 = 1
	p2pIDExecTimeMapping := make(map[string]int64)
	for p2pID, node := range NodeCatalog {
		// skip the overload node
		if node.Overload {
			continue
		}
		if _, exist := node.AvailableFunctionsReplicas[functionName]; exist {
			// no execTime record yet, just gave one (in fact it already init as 1)
			// execTime := time.Duration(1)
			// if t, exist := node.FunctionExecutionTime[functionName]; exist {
			// execTime = t
			execTime := node.FunctionExecutionTime[functionName].Load()
			execTimeProd *= execTime
			// }
			p2pIDExecTimeMapping[p2pID] = execTime
		}
	}
	// leftProd := []time.Duration{1}
	// execTimeList := make([]time.Duration, 0)
	// p2pIDList := make([]string, 0)
	// for p2pID, node := range NodeCatalog {
	// 	// skip the overload node
	// 	if node.Overload {
	// 		continue
	// 	}
	// 	if _, exist := node.AvailableFunctionsReplicas[functionName]; exist {
	// 		// default exec time, no execTime record yet, just give the fastest time
	// 		execTime := time.Duration(1)
	// 		if t, exist := node.FunctionExecutionTime[functionName]; exist {
	// 			// execTimeProd *= execTime
	// 			execTime = t
	// 		}
	// 		leftProd = append(leftProd, leftProd[len(leftProd)-1]*execTime)
	// 		execTimeList = append(execTimeList, execTime)
	// 		p2pIDList = append(p2pIDList, p2pID)
	// 	}
	// }
	// all the node with funtion is overload
	if len(p2pIDExecTimeMapping) == 0 {
		return "", fmt.Errorf("no non-overloaded node to execution function: %s", functionName)
	}

	choices := make([]weightedrand.Choice[string, int64], 0)
	// rightProd := make([]time.Duration, len(leftProd))
	// rightProd[len(leftProd)-1] = 1
	// for i := len(execTimeList) - 1; i >= 0; i-- {
	// 	choices = append(choices, weightedrand.NewChoice(p2pIDList[i], rightProd[i]*leftProd[i]))
	// 	rightProd[i-1] = rightProd[i] * execTimeList[i]
	// }

	for p2pID, execTime := range p2pIDExecTimeMapping {
		probability := execTimeProd / execTime
		choices = append(choices, weightedrand.NewChoice(p2pID, probability))
		// fmt.Printf("exec time map %s: %s (probability: %s)\n", p2pID, execTime, probability)
	}
	chooser, _ := weightedrand.NewChooser(
		choices...,
	)

	return chooser.Pick(), nil
}

// find the information of functionstatus, and found the p2pid can be deployed/triggered this function
// if the first parameter is nil, mean do not require deploy before trigger
func findSuitableNode(functionName string, c catalog.Catalog) (string, error) {

	p2pID, err := weightExecTimeScheduler(functionName, c.NodeCatalog)
	// if can not found the suitable node to execute function, report the first non-overload node
	if err != nil {
		for p2pID, node := range c.NodeCatalog {
			if !node.Overload {
				return p2pID, nil
			}
		}
	}

	// why don't change it to map?
	return p2pID, nil

	// for _, mapping := range faasP2PMappingList {
	// 	overload := c.NodeCatalog[mapping.P2PID].Overload
	// 	if _, exist := c.NodeCatalog[mapping.P2PID].AvailableFunctionsReplicas[functionName]; exist {
	// 		targetFunction = c.FunctionCatalog[functionName]
	// 		// this is where the function call be trigger (already have function on it)
	// 		if !overload {
	// 			log.Printf("found the function %s at host %s\n", functionName, mapping.P2PID)
	// 			return nil, mapping, nil
	// 		}
	// 		break
	// 	}
	// 	// }
	// 	// mean no function found, but the cluster is available
	// 	if !overload && availableNode == nil {
	// 		availableNode = &mapping
	// 	}
	// }
	// if targetFunction == nil {
	// 	err := fmt.Errorf("no endpoints available for: %s", functionName)
	// 	return nil, *availableNode, err
	// }
	// log.Printf("deploy found function %s at %s\n", functionName, availableNode.P2PID)
	// mean no free cluster with function found, but function are somewhere, so deploy on the available cluster
	// return targetFunction, *availableNode, nil
}

// maybe in other place when the platform is overload the request can be redirect
func offloadRequest(w http.ResponseWriter, r *http.Request, config types.FaaSConfig, resolver proxy.BaseURLResolver, functionExecutionTime map[string]*atomic.Int64) {
	if resolver == nil {
		panic("NewHandlerFunc: empty proxy handler resolver, cannot be nil")
	}
	// a http client for sending request
	proxyClient := proxy.NewProxyClientFromConfig(config)

	reverseProxy := httputil.ReverseProxy{}
	reverseProxy.Director = func(req *http.Request) {
		// At least an empty director is required to prevent runtime errors.
		req.URL.Scheme = "http"
	}
	reverseProxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
	}

	// Errors are common during disconnect of client, no need to log them.
	reverseProxy.ErrorLog = log.New(io.Discard, "", 0)

	if r.Body != nil {
		defer r.Body.Close()
	}

	// only allow the Get and Post request
	if r.Method == http.MethodPost || r.Method == http.MethodGet {
		proxyRequest(w, r, proxyClient, resolver, &reverseProxy, functionExecutionTime)
	} else {
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

// basically the below is just copy paste from "github.com/openfaas/faas-provider/proxy"
const (
	defaultContentType     = "text/plain"
	openFaaSInternalHeader = "X-OpenFaaS-Internal"
)

// proxyRequest handles the actual resolution of and then request to the function service.
func proxyRequest(w http.ResponseWriter, originalReq *http.Request, proxyClient *http.Client, resolver proxy.BaseURLResolver, reverseProxy *httputil.ReverseProxy, functionExecutionTime map[string]*atomic.Int64) {
	ctx := originalReq.Context()

	pathVars := mux.Vars(originalReq)
	functionName := pathVars["name"]
	if functionName == "" {
		w.Header().Add(openFaaSInternalHeader, "proxy")

		fhttputil.Errorf(w, http.StatusBadRequest, "Provide function name in the request path")
		return
	}

	functionAddr, err := resolver.Resolve(functionName)
	// fmt.Printf("Function Address: %+v\n\n", functionAddr)
	// fmt.Printf("Original Request: %+v\n\n", originalReq)
	if err != nil {
		w.Header().Add(openFaaSInternalHeader, "proxy")

		// TODO: Should record the 404/not found error in Prometheus.
		log.Printf("resolver error: no endpoints for %s: %s\n", functionName, err.Error())
		fhttputil.Errorf(w, http.StatusServiceUnavailable, "No endpoints available for: %s.", functionName)
		return
	}

	proxyReq, err := buildProxyRequest(originalReq, functionAddr, "/function/"+functionName)
	// fmt.Printf("\nProxy Req: %+v\n", proxyReq)
	if err != nil {

		w.Header().Add(openFaaSInternalHeader, "proxy")

		fhttputil.Errorf(w, http.StatusInternalServerError, "Failed to resolve service: %s.", functionName)
		return
	}

	if proxyReq.Body != nil {
		defer proxyReq.Body.Close()
	}

	start := time.Now()
	defer func() {
		seconds := time.Since(start)
		// log.Printf("%s took %f seconds\n", functionName, seconds.Seconds())
		actualFunctionName := functionName
		if strings.Contains(functionName, ".") {
			actualFunctionName = strings.TrimSuffix(functionName, "."+faasd.DefaultFunctionNamespace)
		}
		// does the update too often?
		functionExecutionTime[actualFunctionName].Store(int64(seconds))
		// switch val := resolver.(type) {
		// case *catalog.FaasClient:
		// 	// log.Printf("P2PID %s for exe time %s\n", val.P2PID, seconds)
		// 	c.NodeCatalog[val.P2PID].FunctionExecutionTime[actualFunctionName].Store(int64(seconds))
		// case *InvokeResolver:
		// 	c.NodeCatalog[c.GetSelfCatalogKey()].FunctionExecutionTime[actualFunctionName].Store(int64(seconds))
		// default:
		// 	log.Printf("Failed to find the type of resolver when recording execution time.\n")
		// }
	}()

	if v := originalReq.Header.Get("Accept"); v == "text/event-stream" {
		originalReq.URL = proxyReq.URL

		reverseProxy.ServeHTTP(w, originalReq)
		return
	}

	response, err := proxyClient.Do(proxyReq.WithContext(ctx))
	if err != nil {
		log.Printf("\nerror with proxy request to: %s, %s\n", proxyReq.URL.String(), err.Error())

		w.Header().Add(openFaaSInternalHeader, "proxy")

		fhttputil.Errorf(w, http.StatusInternalServerError, "Can't reach service for: %s.", functionName)
		return
	}

	if response.Body != nil {
		defer response.Body.Close()
	}

	clientHeader := w.Header()
	copyHeaders(clientHeader, &response.Header)
	w.Header().Set("Content-Type", getContentType(originalReq.Header, response.Header))

	w.WriteHeader(response.StatusCode)
	if response.Body != nil {
		io.Copy(w, response.Body)
	}
}

// buildProxyRequest creates a request object for the proxy request, it will ensure that
// the original request headers are preserved as well as setting openfaas system headers
func buildProxyRequest(originalReq *http.Request, baseURL url.URL, extraPath string) (*http.Request, error) {

	host := baseURL.Host
	// if baseURL.Port() == "" {
	// 	host = baseURL.Host + ":" + watchdogPort
	// }

	url := url.URL{
		Scheme:   baseURL.Scheme,
		Host:     host,
		Path:     extraPath,
		RawQuery: originalReq.URL.RawQuery,
	}

	upstreamReq, err := http.NewRequest(originalReq.Method, url.String(), nil)
	if err != nil {
		return nil, err
	}
	copyHeaders(upstreamReq.Header, &originalReq.Header)

	if len(originalReq.Host) > 0 && upstreamReq.Header.Get("X-Forwarded-Host") == "" {
		upstreamReq.Header["X-Forwarded-Host"] = []string{originalReq.Host}
	}
	if upstreamReq.Header.Get("X-Forwarded-For") == "" {
		upstreamReq.Header["X-Forwarded-For"] = []string{originalReq.RemoteAddr}
	}

	if originalReq.Body != nil {
		upstreamReq.Body = originalReq.Body
	}

	return upstreamReq, nil
}

// copyHeaders clones the header values from the source into the destination.
func copyHeaders(destination http.Header, source *http.Header) {
	for k, v := range *source {
		vClone := make([]string, len(v))
		copy(vClone, v)
		destination[k] = vClone
	}
}

// getContentType resolves the correct Content-Type for a proxied function.
func getContentType(request http.Header, proxyResponse http.Header) (headerContentType string) {
	responseHeader := proxyResponse.Get("Content-Type")
	requestHeader := request.Get("Content-Type")

	if len(responseHeader) > 0 {
		headerContentType = responseHeader
	} else if len(requestHeader) > 0 {
		headerContentType = requestHeader
	} else {
		headerContentType = defaultContentType
	}

	return headerContentType
}
