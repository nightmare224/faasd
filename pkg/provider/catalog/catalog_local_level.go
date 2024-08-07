package catalog

import (
	"context"
	"fmt"
	"log"
	"sync/atomic"
	"time"

	"github.com/containerd/containerd"
	"github.com/containerd/containerd/namespaces"
	gocni "github.com/containerd/go-cni"
	"github.com/openfaas/faas-provider/types"
	faasd "github.com/openfaas/faasd/pkg"
	"github.com/openfaas/faasd/pkg/cninetwork"
	"github.com/openfaas/faasd/pkg/service"
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
	node.FunctionExecutionTime[functionStatus.Name] = new(atomic.Int64)
	node.FunctionExecutionTime[functionStatus.Name].Store(1)
	node.AvailableFunctionsReplicas[functionStatus.Name] = functionStatus.AvailableReplicas
}

func (node *Node) updateAvailableFunctions(functionStatus types.FunctionStatus) {
	node.AvailableFunctionsReplicas[functionStatus.Name] = functionStatus.AvailableReplicas
}

func (node *Node) deleteAvailableFunctions(functionName string) {
	delete(node.AvailableFunctionsReplicas, functionName)
	// keep the record
	// delete(node.FunctionExecutionTime, functionName)
}

// func (node *Node) UpdatePressure(overload bool) {
// 	node.Overload = overload

// 	node.publishInfo()
// }

func (node *Node) ListenUpdateInfo(clientContainerd *containerd.Client, cni gocni.CNI, clientProm *promv1.API, invokeCache map[string]string) {
	for {
		// make sure the available container is running

		// current disable the local replica health monitor
		if node.updateAvailableReplicas(clientContainerd, cni, invokeCache) || node.updatePressure(clientProm) {
			node.publishInfo()
		}
		time.Sleep(infoUpdateIntervalSec * time.Second)
	}

}

func (node *Node) updateAvailableReplicas(client *containerd.Client, cni gocni.CNI, invokeCache map[string]string) bool {

	ctx := namespaces.WithNamespace(context.Background(), faasd.DefaultFunctionNamespace)
	for fname, replica := range node.AvailableFunctionsReplicas {
		// scale down make replica to zero, if want to scale up from zero cause raise condition
		if replica == 0 {
			// make sure the container is remove when the available replicas is zero
			cninetwork.DeleteCNINetwork(ctx, cni, client, fname)
			service.Remove(ctx, client, fname)
		} else {
			err := service.EnsureTaskRunning(ctx, client, cni, fname)
			if err != nil {
				log.Printf("Ensure task running failed: %v", err)
				// TODO: delete the entire container and deploy again if ensure task running failed
			}
			if ip, err := service.GetTaskIP(ctx, client, cni, fname); err == nil {
				invokeCache[fname] = ip
			}
		}
	}
	// containers, err := client.Containers(ctx)
	// if err != nil {
	// 	log.Printf("Listing all containers failed: %v", err)
	// }
	// after makeing all container run, if there is the container exist but not in record, add it
	// will make the deleting container back alive, so have to make sure it is in running state
	// for _, c := range containers {
	// 	name := c.ID()
	// 	if replica, exist := node.AvailableFunctionsReplicas[name]; !exist || replica == 0 {
	// 		node.AvailableFunctionsReplicas[name] = 1
	// 		return true
	// 	}
	// }

	// also failed, the delete is too slow
	// tasks, err := service.GetTaskRunning(ctx, client, cni)
	// if err != nil {
	// 	log.Printf("Listing all containers failed: %v", err)
	// 	return false
	// }
	// log.Printf("running task: %v\n", tasks)
	updated := false
	// for _, name := range tasks {
	// 	if replica, exist := node.AvailableFunctionsReplicas[name]; !exist || replica == 0 {
	// 		node.AvailableFunctionsReplicas[name] = 1
	// 		updated = true
	// 	}
	// }

	service.EnsureAllStoppedTaskDelete(ctx, client, cni)

	// temperatory do not change the number of replica if anything failed
	return updated
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
	cpuQuery := "1 - (rate(node_cpu_seconds_total{mode=\"idle\"}[30s]))"
	CPULoad, err := queryResourceAverageLoad(client, ctx, cpuQuery)
	if err != nil {
		log.Fatalf("CPU usage unavailable from Prometheus: %v", err)
		return updated
	}
	overload_update := (CPULoad > CPUOverloadThreshold)
	// memory
	// memQuery := "1 - avg_over_time(node_memory_MemAvailable_bytes[30s])/node_memory_MemTotal_bytes"
	memQuery := "1 - ((avg_over_time(node_memory_MemFree_bytes[30s]) + avg_over_time(node_memory_Cached_bytes[30s]) + avg_over_time(node_memory_Buffers_bytes[30s])) / node_memory_MemTotal_bytes)"
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
