package catalog

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"

	"github.com/libp2p/go-libp2p/core/network"
	"github.com/openfaas/faas-provider/types"
)

func (c Catalog) ListAvailableFunctions(infoLevel InfoLevel) []types.FunctionStatus {
	var functionStatus []types.FunctionStatus
	functionNameSet := make(map[string]struct{})
	switch infoLevel {
	case LocalLevel:
		for _, fn := range c[selfCatagoryKey].AvailableFunctions {
			if _, exist := functionNameSet[fn.Name]; !exist {
				functionStatus = append(functionStatus, fn)
				functionNameSet[fn.Name] = struct{}{}
			}
		}
	case ClusterLevel:
		for _, node := range c {
			// if id == selfCatagoryKey {
			// 	continue
			// }
			for _, fn := range node.AvailableFunctions {
				if _, exist := functionNameSet[fn.Name]; !exist {
					functionStatus = append(functionStatus, fn)
					functionNameSet[fn.Name] = struct{}{}
				}
			}
		}
	}
	return functionStatus
}

func (c Catalog) AddAvailableFunctions(functionStatus types.FunctionStatus) {
	// update the catalog of itself
	c[selfCatagoryKey].addAvailableFunctions(functionStatus)

	// update the overall replicat count
	c.updatetReplicasWithFunctionName(functionStatus.Name)

	// publish info
	c[selfCatagoryKey].publishInfo()
}

func (c Catalog) DeleteAvailableFunctions(functionName string) {
	// update the catalog of itself
	c[selfCatagoryKey].deleteAvailableFunctions(functionName)

	// update the overall replicat count
	c.updatetReplicasWithFunctionName(functionName)

	// publish info
	c[selfCatagoryKey].publishInfo()
}

// available replicas means the replicas on current node, replicas means the replicas
// in the entire p2p network
func (c Catalog) updatetReplicasWithFunctionName(functionName string) {
	var replicas uint64 = 0
	updateFns := make([]*types.FunctionStatus, 0)
	for _, node := range c {
		for _, fn := range node.AvailableFunctions {
			if functionName == fn.Name {
				replicas += fn.AvailableReplicas
				updateFns = append(updateFns, &fn)
			}
		}
	}
	fmt.Printf("Update function: %s to replicas %d\n", functionName, replicas)
	for _, fn := range updateFns {
		fn.Replicas = uint64(replicas)
	}
}

// update the total replicas with the given nodeinfo
func (c Catalog) updatetReplicasWithNodeInfo(info NodeInfo) {
	for _, fn := range info.AvailableFunctions {
		c.updatetReplicasWithFunctionName(fn.Name)
	}

}

// available replicas means the replicas on current node, replicas means the replicas
// in the entire p2p network
// func (c Catalog) UpdatetReplicas(functionName string) {

// }

// the handler of
func (c Catalog) streamAvailableFunctions(stream network.Stream) {
	defer stream.Close()
	var buf bytes.Buffer
	if _, err := io.Copy(&buf, stream); err != nil {
		log.Fatalf("Failed to read from stream: %v", err)
		return
	}
	// TODO: receive the initialize available function
	info := new(NodeInfo)
	err := json.Unmarshal(buf.Bytes(), info)
	if err != nil {
		log.Printf("deserialized info message error: %s\n", err)
		return
	}
	fmt.Println("Receive info from publisher stream:", info)

	// update the info in the node
	c[stream.Conn().RemotePeer().String()].NodeInfo = *info

	// example
	// buf := make([]byte, 256)
	// n, err := stream.Read(buf)
	// if err != nil && err != io.EOF {
	// 	log.Fatalf("Failed to read from stream: %v", err)
	// }

	// message := string(buf[:n])
	// fmt.Printf("Received direct message: %s\n", message)
}
