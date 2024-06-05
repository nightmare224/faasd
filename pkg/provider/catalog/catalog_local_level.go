package catalog

import (
	"context"
	"fmt"
	"log"
	"sync/atomic"
	"time"

	"github.com/containerd/containerd"
	"github.com/openfaas/faas-provider/types"
	promv1 "github.com/prometheus/client_golang/api/prometheus/v1"

	// v1 "github.com/prometheus/client_golang/api/prometheus/v1"
	"github.com/prometheus/common/model"
)

func (node *Node) addAvailableFunctions(functionStatus types.FunctionStatus) {
	// for _, fn := range node.AvailableFunctions {
	// 	if functionStatus.Name == fn.Name {
	// 		return
	// 	}
	// }
	// functionSet := append(node.AvailableFunctions, functionStatus)
	// node.AvailableFunctions = functionSet
	node.AvailableFunctionsReplicas[functionStatus.Name] = functionStatus.AvailableReplicas
	node.FunctionExecutionTime[functionStatus.Name] = new(atomic.Int64)
	node.FunctionExecutionTime[functionStatus.Name].Store(1)
}

func (node *Node) deleteAvailableFunctions(functionName string) {
	// for i, fn := range node.AvailableFunctions {
	// 	if functionName == fn.Name {
	// 		node.AvailableFunctions = append(node.AvailableFunctions[:i], node.AvailableFunctions[i+1:]...)
	// 	}
	// }
	delete(node.AvailableFunctionsReplicas, functionName)
	delete(node.FunctionExecutionTime, functionName)
}

// func (node *Node) UpdatePressure(overload bool) {
// 	node.Overload = overload

// 	node.publishInfo()
// }

func (node *Node) ListenUpdateInfo(clientContainerd *containerd.Client, clientProm *promv1.API) {
	for {
		// current disable the local replica health monitor
		if /* node.updateAvailableReplicas(clientContainerd) || */ node.updatePressure(clientProm) {
			node.publishInfo()
		}
		time.Sleep(infoUpdateIntervalSec * time.Second)
	}

}

// preodically update the available repicas, based on the running info of the containerd
// func (node *Node) updateAvailableReplicas(client *containerd.Client) bool {
// 	updated := false
// 	ctx := namespaces.WithNamespace(context.Background(), faasd.DefaultFunctionNamespace)
// 	for _, fn := range node.AvailableFunctions {
// 		replicas := 0
// 		c, err := client.LoadContainer(ctx, fn.Name)
// 		if err != nil {
// 			fmt.Printf("unable to find function: %s, error %s", fn.Name, err)
// 			continue
// 		}
// 		task, err := c.Task(ctx, nil)
// 		if err != nil {
// 			fmt.Printf("unable to get task: %s, error %s", fn.Name, err)
// 			continue
// 		}
// 		svc, err := task.Status(ctx)
// 		if err != nil {
// 			fmt.Printf("unable to get task status for container: %s, error: %s", fn.Name, err)
// 			continue
// 		}
// 		if svc.Status == "running" {
// 			replicas = 1
// 		}
// 		if uint64(replicas) != fn.AvailableReplicas {
// 			fn.AvailableReplicas = uint64(replicas)
// 			updated = true
// 		}
// 	}
// 	return updated
// }

// preodically update the available repicas, based on the running info of the containerd
// func (node *Node) updateAvailableReplicasWithFunctionName(client *containerd.Client, functionName string) bool {
// 	updated := false
// 	ctx := namespaces.WithNamespace(context.Background(), faasd.DefaultFunctionNamespace)
// 	for _, fn := range node.AvailableFunctions {
// 		if functionName == fn.Name {
// 			replicas := 0
// 			c, err := client.LoadContainer(ctx, fn.Name)
// 			if err != nil {
// 				fmt.Printf("unable to find function: %s, error %s", fn.Name, err)
// 				break
// 			}
// 			task, err := c.Task(ctx, nil)
// 			if err != nil {
// 				fmt.Printf("unable to get task: %s, error %s", fn.Name, err)
// 				break
// 			}
// 			svc, err := task.Status(ctx)
// 			if err != nil {
// 				fmt.Printf("unable to get task status for container: %s, error: %s", fn.Name, err)
// 				break
// 			}
// 			if svc.Status == "running" {
// 				replicas = 1
// 			}
// 			if uint64(replicas) != fn.AvailableReplicas {
// 				fn.AvailableReplicas = uint64(replicas)
// 				updated = true
// 			}
// 			break
// 		}
// 	}
// 	return updated
// }

// add the overloaded infomation in it
func (node *Node) updatePressure(client *promv1.API) bool {
	updated := false
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	// start query
	// cpu
	cpuQuery := "1 - (rate(node_cpu_seconds_total{mode=\"idle\"}[1m]))"
	CPULoad, err := queryResourceAverageLoad(client, ctx, cpuQuery)
	if err != nil {
		log.Fatalf("CPU usage unavailable from Prometheus: %v", err)
		return updated
	}
	overload_update := (CPULoad > CPUOverloadThreshold)
	// memory
	// memQuery := "1 - avg_over_time(node_memory_MemAvailable_bytes[1m])/node_memory_MemTotal_bytes"
	memQuery := "1 - ((avg_over_time(node_memory_MemFree_bytes[1m]) + avg_over_time(node_memory_Cached_bytes[1m]) + avg_over_time(node_memory_Buffers_bytes[1m])) / node_memory_MemTotal_bytes)"
	MemLoad, err := queryResourceAverageLoad(client, ctx, memQuery)
	if err != nil {
		log.Fatalf("memory usage unavailable from Prometheus: %v", err)
		return updated
	}
	overload_update = overload_update || (MemLoad > MemOverloadThreshold)
	// time.Sleep(time.Second * 10)
	// fmt.Println("The update overload: ", overload_update)
	// update
	if overload_update != node.Overload {
		node.Overload = overload_update
		updated = true
	}
	return updated
}

// func getPrometh
func queryResourceAverageLoad(promClient *promv1.API, ctx context.Context, query string) (model.SampleValue, error) {

	result, _, err := (*promClient).Query(ctx, query, time.Now(), promv1.WithTimeout(5*time.Second))
	if err != nil {
		err := fmt.Errorf("error querying Prometheus: %v", err)
		return 0, err
	}

	switch {
	case result.Type() == model.ValVector:
		var avgLoad model.SampleValue = 0
		vectorVal := result.(model.Vector)
		for _, elem := range vectorVal {
			avgLoad += elem.Value
		}
		return avgLoad / model.SampleValue(len(vectorVal)), nil
	default:
		err := fmt.Errorf("unexpected value type %q", result.Type())
		return 0, err
	}
}

func (node *Node) publishInfo() {

	node.infoChan <- &node.NodeInfo
}
