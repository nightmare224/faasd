package catalog

import (
	"context"
	"fmt"
	"time"

	"github.com/containerd/containerd"
	"github.com/containerd/containerd/namespaces"
	"github.com/openfaas/faas-provider/types"
)

func (node *Node) AddAvailableFunctions(functionStatus types.FunctionStatus) {
	for _, fn := range node.AvailableFunctions {
		if functionStatus.Name == fn.Name {
			return
		}
	}
	functionSet := append(node.AvailableFunctions, functionStatus)
	node.AvailableFunctions = functionSet

	node.publishInfo()
}

func (node *Node) DeleteAvailableFunctions(functionName string) {
	for i, fn := range node.AvailableFunctions {
		if functionName == fn.Name {
			node.AvailableFunctions = append(node.AvailableFunctions[:i], node.AvailableFunctions[i+1:]...)
		}
	}

	node.publishInfo()
}

func (node *Node) UpdatePressure(overload bool) {
	node.Overload = overload

	node.publishInfo()
}

// preodically update the available repicas, based on the running info of the containerd
func (node *Node) ListenAvailableReplicas(client *containerd.Client, functionName string, namespace string) {
	for {
		ctx := namespaces.WithNamespace(context.Background(), namespace)
		for _, fn := range node.AvailableFunctions {
			replicas := 0
			c, err := client.LoadContainer(ctx, fn.Name)
			if err != nil {
				fmt.Printf("unable to find function: %s, error %s", fn.Name, err)
				fn.AvailableReplicas = 0
				return
			}
			task, err := c.Task(ctx, nil)
			if err != nil {
				fmt.Printf("unable to get task: %s, error %s", fn.Name, err)
				fn.AvailableReplicas = 0
				return
			}
			svc, err := task.Status(ctx)
			if err != nil {
				fmt.Printf("unable to get task status for container: %s, error: %s", fn.Name, err)
				fn.AvailableReplicas = 0
				return
			}
			if svc.Status == "running" {
				replicas = 1
			}
			fn.AvailableReplicas = uint64(replicas)
		}
		time.Sleep(infoUpdateIntervalSec * time.Second)
	}
}

func (node *Node) publishInfo() {
	node.infoChan <- &node.NodeInfo
}
