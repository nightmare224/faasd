package handlers

import (
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync/atomic"
	"time"

	"github.com/containerd/containerd"
	gocni "github.com/containerd/go-cni"
	"github.com/gorilla/mux"
	weightedrand "github.com/mroth/weightedrand/v2"
	fhttputil "github.com/openfaas/faas-provider/httputil"
	"github.com/openfaas/faas-provider/proxy"
	"github.com/openfaas/faas-provider/types"
	faasd "github.com/openfaas/faasd/pkg"
	"github.com/openfaas/faasd/pkg/provider/catalog"
)

// The factory that affect the weighted round robin (exponential)
const weightRRfactory = 4
const overloadPenaltyFactory = 4

func MakeTriggerHandler(config types.FaaSConfig, resolver proxy.BaseURLResolver, client *containerd.Client, cni gocni.CNI, secretMountPath string, c catalog.Catalog) http.HandlerFunc {

	// enableOffload := true
	return func(w http.ResponseWriter, r *http.Request) {
		vars := mux.Vars(r)
		functionName := vars["name"]
		if strings.Contains(vars["name"], ".") {
			functionName = strings.TrimSuffix(vars["name"], "."+faasd.DefaultFunctionNamespace)
		}
		_, exist := c.FunctionCatalog[functionName]
		if !exist {
			err := fmt.Errorf("no endpoints available for: %s", functionName)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		targetP2PID := catalog.GetSelfCatalogKey()
		var err error
		if !isOffloadRequest(r) {
			targetP2PID, err = findSuitableNode(functionName, c)
			// the found node do not have function yet, required deployed first (implict scale up)
			if err != nil {
				log.Printf("The trigger node may not have function yet: %v\n", err.Error())
				// deploy on local if no available tr
				deployFunctionByP2PID(faasd.DefaultFunctionNamespace, functionName, client, cni, secretMountPath, targetP2PID, c)
				// wait until the function is ready
				fn, err := waitDeployReadyAndReport(client, c.NodeCatalog[targetP2PID].FaasClient, functionName)
				if err != nil {
					log.Printf("[Deploy] error deploying %s, error: %s\n", functionName, err)
					http.Error(w, err.Error(), http.StatusInternalServerError)
					return
				}
				if targetP2PID == catalog.GetSelfCatalogKey() {
					c.AddAvailableFunctions(fn)
				}
			}
			// TODO: should only do if it is not !isOffloadRequest
			defer func() {
				potentailP2PID, err := explorePotentialNode(targetP2PID, functionName, c)
				if err != nil {
					return
				}
				go func() {
					deployFunctionByP2PID(faasd.DefaultFunctionNamespace, functionName, client, cni, secretMountPath, potentailP2PID, c)
					if potentailP2PID == catalog.GetSelfCatalogKey() {
						fn, err := waitDeployReadyAndReport(client, c.NodeCatalog[catalog.GetSelfCatalogKey()].FaasClient, functionName)
						if err != nil {
							log.Printf("[Deploy] error deploying %s, error: %s\n", functionName, err)
							return
						}
						c.AddAvailableFunctions(fn)
					}
				}()
			}()
		}

		// trigger the function here
		// if non local than change the resolver
		invokeResolver := resolver
		if targetP2PID != catalog.GetSelfCatalogKey() {
			invokeResolver = &c.NodeCatalog[targetP2PID].FaasClient
			markAsOffloadRequest(r)
		}
		offloadRequest(w, r, config, invokeResolver, c.NodeCatalog[targetP2PID].FunctionExecutionTime)

		// create a replica at the trigger point
		// if replicas, exist := c.NodeCatalog[catalog.GetSelfCatalogKey()].AvailableFunctionsReplicas[functionName]; !exist || replicas == 0 {
		// 	go func() {
		// 		deployFunctionByP2PID(faasd.DefaultFunctionNamespace, functionName, client, cni, secretMountPath, catalog.GetSelfCatalogKey(), c)
		// 		fn, err := waitDeployReadyAndReport(client, c.NodeCatalog[catalog.GetSelfCatalogKey()].FaasClient, functionName)
		// 		if err != nil {
		// 			log.Printf("[Deploy] error deploying %s, error: %s\n", functionName, err)
		// 			return
		// 		}
		// 		c.AddAvailableFunctions(fn)
		// 	}()
		// }

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
func explorePotentialNode(targetP2PID string, functionName string, c catalog.Catalog) (string, error) {
	currExecTime := c.NodeCatalog[targetP2PID].FunctionExecutionTime[functionName].Load()
	potentialP2PID := ""
	for _, p2pID := range *c.SortedP2PID {
		// if nodeCatalog
		node := c.NodeCatalog[p2pID]
		if replicas, exist := node.AvailableFunctionsReplicas[functionName]; !exist || replicas == 0 {
			if _, exist := node.FunctionExecutionTime[functionName]; exist {
				execTime := node.FunctionExecutionTime[functionName].Load()
				if execTime < currExecTime {
					return p2pID, nil
				}
				// log.Printf("p2pID: %s, execTime: %d, currExecTime: %d\n", p2pID, execTime, currExecTime)
			} else if potentialP2PID == "" {
				potentialP2PID = p2pID
			}
		}
	}
	if potentialP2PID == "" {
		return "", fmt.Errorf("no potential node for function: %s", functionName)
	}
	return potentialP2PID, nil
}

// return the select p2pid to execution function based on last weighted exec time
func weightExecTimeScheduler(functionName string, nodeCatalog map[string]*catalog.Node) (string, error) {

	var execTimeProd float64 = 1
	var execTimeMin float64 = math.MaxFloat64
	p2pIDExecTimeRawMapping := make(map[string]float64)
	for p2pID, node := range nodeCatalog {

		if replicas, exist := node.AvailableFunctionsReplicas[functionName]; exist && replicas > 0 {
			execTimeRaw := float64(node.FunctionExecutionTime[functionName].Load())
			if execTimeRaw == 1 {
				// log.Printf("Host %s has exectime = 1\n", p2pID)
				return p2pID, nil
			}
			if execTimeRaw < execTimeMin {
				execTimeMin = execTimeRaw
			}
			p2pIDExecTimeRawMapping[p2pID] = execTimeRaw
		}
	}
	// log.Printf("Minimal: %f\np2pIDExecTimeRawMapping: %v\n", execTimeMin, p2pIDExecTimeRawMapping)

	p2pIDExecTimeMapping := make(map[string]float64)
	for p2pID, execTimeRaw := range p2pIDExecTimeRawMapping {
		execTime := float64(execTimeRaw) / float64(execTimeMin)
		factory := weightRRfactory
		if nodeCatalog[p2pID].Overload {
			// gave the extra penalty if the target node is overload
			factory += overloadPenaltyFactory
		}
		execTimeWeighted := execTime
		for i := 1; i < factory; i++ {
			execTimeWeighted *= execTime
		}
		execTimeProd *= execTimeWeighted
		p2pIDExecTimeMapping[p2pID] = execTimeWeighted
	}

	// all the node with funtion is overload
	if len(p2pIDExecTimeMapping) == 0 {
		return "", fmt.Errorf("no node to execution function: %s", functionName)
	}

	choices := make([]weightedrand.Choice[string, uint64], 0)

	for p2pID, execTime := range p2pIDExecTimeMapping {
		probability := uint64(execTimeProd / execTime * 10)
		choices = append(choices, weightedrand.NewChoice(p2pID, probability))
		// fmt.Printf("exec time map %s: %f (probability: %d)\n", p2pID, p2pIDExecTimeRawMapping[p2pID], probability)
	}
	chooser, errChoose := weightedrand.NewChooser(
		choices...,
	)
	if errChoose != nil {
		log.Println("error when making choice, maybe due to overflow")
		for _, node := range nodeCatalog {
			if replicas, exist := node.AvailableFunctionsReplicas[functionName]; exist && replicas > 0 {
				// reset
				node.FunctionExecutionTime[functionName].Store(1)
			}
		}
		// return itself for error handle
		return catalog.GetSelfCatalogKey(), nil
	}

	return chooser.Pick(), nil
}

// find the information of functionstatus, and found the p2pid can be deployed/triggered this function
// if the first parameter is nil, mean do not require deploy before trigger
func findSuitableNode(functionName string, c catalog.Catalog) (string, error) {

	p2pID, err := weightExecTimeScheduler(functionName, c.NodeCatalog)
	// if can not found the suitable node to execute function, report the first non-overload node
	// should access based on the rtt sequence, the map do not guarantee the iterated sequence of map
	if err != nil {
		for _, p2pID := range *c.SortedP2PID {
			if !c.NodeCatalog[p2pID].Overload {
				return p2pID, err
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
		nanoseconds := time.Since(start)
		// log.Printf("%s took %f seconds\n", functionName, seconds.Seconds())
		actualFunctionName := functionName
		if strings.Contains(functionName, ".") {
			actualFunctionName = strings.TrimSuffix(functionName, "."+faasd.DefaultFunctionNamespace)
		}
		// does the update too often?
		// just store milliseconds
		functionExecutionTime[actualFunctionName].Store(int64(nanoseconds / 1000000))
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
