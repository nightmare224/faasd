package handlers

import (
	"net/http"

	"github.com/openfaas/faas-provider/proxy"
	"github.com/openfaas/faas-provider/types"
	"github.com/openfaas/faasd/pkg/provider/catalog"
	"github.com/openfaas/go-sdk"
)

func MakeTriggerHandler(config types.FaaSConfig, resolver proxy.BaseURLResolver, externalClients []*sdk.Client, c catalog.Catalog) http.HandlerFunc {

	// return proxy.NewHandlerFunc(config, resolver, false)
	return func(w http.ResponseWriter, r *http.Request) {
		offload := true
		if offload {
			// this should be trigger the target faas client
			// OffloadRequest(w, r, config, resolver)
			proxy.NewHandlerFunc(config, resolver, true)(w, r)
		} else {
			proxy.NewHandlerFunc(config, resolver, true)(w, r)
		}
	}
}

// maybe only report the overload status to neighbor while change

// maybe in other place when the platform is overload the request can be redirect
// func OffloadRequest(w http.ResponseWriter, r *http.Request, config types.FaaSConfig, resolver proxy.BaseURLResolver) {
// 	if resolver == nil {
// 		panic("NewHandlerFunc: empty proxy handler resolver, cannot be nil")
// 	}
// 	// a http client for sending request
// 	proxyClient := proxy.NewProxyClientFromConfig(config)

// 	reverseProxy := httputil.ReverseProxy{}
// 	reverseProxy.Director = func(req *http.Request) {
// 		// At least an empty director is required to prevent runtime errors.
// 		req.URL.Scheme = "http"
// 	}
// 	reverseProxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
// 	}

// 	// Errors are common during disconnect of client, no need to log them.
// 	reverseProxy.ErrorLog = log.New(io.Discard, "", 0)

// 	if r.Body != nil {
// 		defer r.Body.Close()
// 	}

// 	// only allow the Get and Post request
// 	if r.Method == http.MethodPost || r.Method == http.MethodGet {
// 		proxyRequest(w, r, proxyClient, resolver, &reverseProxy)
// 	} else {
// 		w.WriteHeader(http.StatusMethodNotAllowed)
// 	}
// }

// // basically the below is just copy paste from "github.com/openfaas/faas-provider/proxy"
// const (
// 	defaultContentType     = "text/plain"
// 	openFaaSInternalHeader = "X-OpenFaaS-Internal"
// )

// // proxyRequest handles the actual resolution of and then request to the function service.
// func proxyRequest(w http.ResponseWriter, originalReq *http.Request, proxyClient *http.Client, resolver proxy.BaseURLResolver, reverseProxy *httputil.ReverseProxy) {
// 	ctx := originalReq.Context()

// 	pathVars := mux.Vars(originalReq)
// 	functionName := pathVars["name"]
// 	if functionName == "" {
// 		w.Header().Add(openFaaSInternalHeader, "proxy")

// 		fhttputil.Errorf(w, http.StatusBadRequest, "Provide function name in the request path")
// 		return
// 	}

// 	functionAddr, err := resolver.Resolve(functionName)
// 	// fmt.Printf("Function Address: %+v\n\n", functionAddr)
// 	// fmt.Printf("Original Request: %+v\n\n", originalReq)
// 	if err != nil {
// 		w.Header().Add(openFaaSInternalHeader, "proxy")

// 		// TODO: Should record the 404/not found error in Prometheus.
// 		log.Printf("resolver error: no endpoints for %s: %s\n", functionName, err.Error())
// 		fhttputil.Errorf(w, http.StatusServiceUnavailable, "No endpoints available for: %s.", functionName)
// 		return
// 	}

// 	proxyReq, err := buildProxyRequest(originalReq, functionAddr, "/function/"+functionName)
// 	// fmt.Printf("\nProxy Req: %+v\n", proxyReq)
// 	if err != nil {

// 		w.Header().Add(openFaaSInternalHeader, "proxy")

// 		fhttputil.Errorf(w, http.StatusInternalServerError, "Failed to resolve service: %s.", functionName)
// 		return
// 	}

// 	if proxyReq.Body != nil {
// 		defer proxyReq.Body.Close()
// 	}

// 	start := time.Now()
// 	defer func() {
// 		seconds := time.Since(start)
// 		log.Printf("%s took %f seconds\n", functionName, seconds.Seconds())
// 	}()

// 	if v := originalReq.Header.Get("Accept"); v == "text/event-stream" {
// 		originalReq.URL = proxyReq.URL

// 		reverseProxy.ServeHTTP(w, originalReq)
// 		return
// 	}

// 	response, err := proxyClient.Do(proxyReq.WithContext(ctx))
// 	if err != nil {
// 		log.Printf("\nerror with proxy request to: %s, %s\n", proxyReq.URL.String(), err.Error())

// 		w.Header().Add(openFaaSInternalHeader, "proxy")

// 		fhttputil.Errorf(w, http.StatusInternalServerError, "Can't reach service for: %s.", functionName)
// 		return
// 	}

// 	if response.Body != nil {
// 		defer response.Body.Close()
// 	}

// 	clientHeader := w.Header()
// 	copyHeaders(clientHeader, &response.Header)
// 	w.Header().Set("Content-Type", getContentType(originalReq.Header, response.Header))

// 	w.WriteHeader(response.StatusCode)
// 	if response.Body != nil {
// 		io.Copy(w, response.Body)
// 	}
// }

// // buildProxyRequest creates a request object for the proxy request, it will ensure that
// // the original request headers are preserved as well as setting openfaas system headers
// func buildProxyRequest(originalReq *http.Request, baseURL url.URL, extraPath string) (*http.Request, error) {

// 	host := baseURL.Host
// 	// if baseURL.Port() == "" {
// 	// 	host = baseURL.Host + ":" + watchdogPort
// 	// }

// 	url := url.URL{
// 		Scheme:   baseURL.Scheme,
// 		Host:     host,
// 		Path:     extraPath,
// 		RawQuery: originalReq.URL.RawQuery,
// 	}

// 	upstreamReq, err := http.NewRequest(originalReq.Method, url.String(), nil)
// 	if err != nil {
// 		return nil, err
// 	}
// 	copyHeaders(upstreamReq.Header, &originalReq.Header)

// 	if len(originalReq.Host) > 0 && upstreamReq.Header.Get("X-Forwarded-Host") == "" {
// 		upstreamReq.Header["X-Forwarded-Host"] = []string{originalReq.Host}
// 	}
// 	if upstreamReq.Header.Get("X-Forwarded-For") == "" {
// 		upstreamReq.Header["X-Forwarded-For"] = []string{originalReq.RemoteAddr}
// 	}

// 	if originalReq.Body != nil {
// 		upstreamReq.Body = originalReq.Body
// 	}

// 	return upstreamReq, nil
// }

// // copyHeaders clones the header values from the source into the destination.
// func copyHeaders(destination http.Header, source *http.Header) {
// 	for k, v := range *source {
// 		vClone := make([]string, len(v))
// 		copy(vClone, v)
// 		destination[k] = vClone
// 	}
// }

// // getContentType resolves the correct Content-Type for a proxied function.
// func getContentType(request http.Header, proxyResponse http.Header) (headerContentType string) {
// 	responseHeader := proxyResponse.Get("Content-Type")
// 	requestHeader := request.Get("Content-Type")

// 	if len(responseHeader) > 0 {
// 		headerContentType = responseHeader
// 	} else if len(requestHeader) > 0 {
// 		headerContentType = requestHeader
// 	} else {
// 		headerContentType = defaultContentType
// 	}

// 	return headerContentType
// }
