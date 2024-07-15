package handlers

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/containerd/containerd"
	faasd "github.com/openfaas/faasd/pkg"
)

const watchdogPort = 8080

type InvokeResolver struct {
	client *containerd.Client
	Cache  map[string]string
}

func NewInvokeResolver(client *containerd.Client) *InvokeResolver {
	return &InvokeResolver{
		client: client,
		Cache:  make(map[string]string),
	}
}

func (i *InvokeResolver) Resolve(functionName string) (url.URL, error) {
	actualFunctionName := functionName

	namespace := getNamespaceOrDefault(functionName, faasd.DefaultFunctionNamespace)

	if strings.Contains(functionName, ".") {
		actualFunctionName = strings.TrimSuffix(functionName, "."+namespace)
	}

	serviceIP, exist := i.Cache[actualFunctionName]
	if !exist {
		function, err := GetFunction(i.client, actualFunctionName, namespace)
		if err != nil {
			return url.URL{}, fmt.Errorf("%s not found", actualFunctionName)
		}
		serviceIP = function.IP
	}

	urlStr := fmt.Sprintf("http://%s:%d", serviceIP, watchdogPort)

	urlRes, err := url.Parse(urlStr)
	if err != nil {
		return url.URL{}, err
	}

	return *urlRes, nil
}

func getNamespaceOrDefault(name, defaultNamespace string) string {
	namespace := defaultNamespace
	if strings.Contains(name, ".") {
		namespace = name[strings.LastIndexAny(name, ".")+1:]
	}
	return namespace
}
