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
	"time"

	"github.com/gorilla/mux"
	fhttputil "github.com/openfaas/faas-provider/httputil"
	"github.com/openfaas/faas-provider/proxy"
	"github.com/openfaas/faas-provider/types"
	faasd "github.com/openfaas/faasd/pkg"
	"github.com/openfaas/faasd/pkg/provider/catalog"
)

func MakeTriggerHandler(config types.FaaSConfig, resolver proxy.BaseURLResolver, faasP2PMappingList []catalog.FaasP2PMapping, c catalog.Catalog) http.HandlerFunc {

	offload := true
	return func(w http.ResponseWriter, r *http.Request) {
		fmt.Printf("Receive the trigger!\n")
		if offload {
			// this should be trigger the target faas client
			vars := mux.Vars(r)
			parts := strings.Split(vars["name"], ".")
			functionName := parts[0]
			targetFunction, targetNodeMapping, err := findSuitableNode(functionName, faasP2PMappingList, c)
			if err != nil {
				fmt.Printf("Unable to trigger function: %v\n", err.Error())
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			// no deploy required, just trigger
			if targetFunction != nil {
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
				targetNodeMapping.FaasClient.Deploy(context.Background(), deployment)
				// TODO: wait unitl the function ready
			}
			// trigger the function here
			// if non local than change the resolver
			invokeResolver := resolver
			if targetNodeMapping.P2PID != c.GetSelfCatalogKey() {
				invokeResolver = &targetNodeMapping
			}
			offloadRequest(w, r, config, invokeResolver, c)

		} else {
			proxy.NewHandlerFunc(config, resolver, true)(w, r)
		}
	}
}

// find the information of functionstatus, and found the one can be deployed/triggered this function
// if the first parameter is nil, mean do not require deploy before trigger
func findSuitableNode(functionName string, faasP2PMappingList []catalog.FaasP2PMapping, c catalog.Catalog) (*types.FunctionStatus, catalog.FaasP2PMapping, error) {
	var targetFunction *types.FunctionStatus = nil
	var availableNode *catalog.FaasP2PMapping = nil
	for _, mapping := range faasP2PMappingList {
		overload := c.NodeCatalog[mapping.P2PID].Overload
		// for _, fn := range c.NodeCatalog[mapping.P2PID].AvailableFunctions {
		// fmt.Printf("target function %s, available func %s.\n", functionName, fn.Name)
		// use or without namespace
		// if functionName == fn.Name || functionName == fmt.Sprintf("%s.%s", fn.Name, fn.Namespace) {
		if _, exist := c.NodeCatalog[mapping.P2PID].AvailableFunctionsReplicas[functionName]; exist {
			targetFunction = c.FunctionCatalog[functionName]
			// this is where the function call be trigger (already have function on it)
			if !overload {
				log.Printf("found the function %s at host %s\n", functionName, mapping.P2PID)
				return nil, mapping, nil
			}
			break
		}
		// }
		// mean no function found, but the cluster is available
		if !overload && availableNode == nil {
			availableNode = &mapping
		}
	}
	if targetFunction == nil {
		err := fmt.Errorf("no endpoints available for: %s", functionName)
		return nil, *availableNode, err
	}
	log.Printf("deploy found function %s at %s\n", functionName, availableNode.P2PID)
	// mean no free cluster with function found, but function are somewhere, so deploy on the available cluster
	return targetFunction, *availableNode, nil
}

// maybe in other place when the platform is overload the request can be redirect
func offloadRequest(w http.ResponseWriter, r *http.Request, config types.FaaSConfig, resolver proxy.BaseURLResolver, c catalog.Catalog) {
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
		proxyRequest(w, r, proxyClient, resolver, &reverseProxy, c)
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
func proxyRequest(w http.ResponseWriter, originalReq *http.Request, proxyClient *http.Client, resolver proxy.BaseURLResolver, reverseProxy *httputil.ReverseProxy, c catalog.Catalog) {
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
		log.Printf("%s took %f seconds\n", functionName, seconds.Seconds())
		actualFunctionName := functionName
		if strings.Contains(functionName, ".") {
			actualFunctionName = strings.TrimSuffix(functionName, "."+faasd.DefaultFunctionNamespace)
		}
		switch val := resolver.(type) {
		case *catalog.FaasP2PMapping:
			log.Printf("P2PID %s for exe time %s\n", val.P2PID, seconds)
			c.NodeCatalog[val.P2PID].FunctionExecutionTime[actualFunctionName] = seconds
		case *InvokeResolver:
			c.NodeCatalog[c.GetSelfCatalogKey()].FunctionExecutionTime[actualFunctionName] = seconds
		default:
			log.Printf("Failed to find the type of resolver when recording execution time.\n")
		}
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
