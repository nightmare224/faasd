package catalog

import (
	"github.com/openfaas/faas-provider/types"
)

// own itself use this key, other will use the p2p id as key
var selfCatagoryKey string = "0"

type InfoLevel int

const (
	LocalLevel InfoLevel = iota
	ClusterLevel
)

type NodeInfo struct {
	AvailableFunctions []types.FunctionStatus `json:"availableFunctions"`
	Overload           bool                   `json:"overload"`
}

// key is the peer ID in string
type Catalog map[string]*Node

type NodeMetadata struct {
	Ip       string `json:"ip"`
	Hostname string `json:"hostname"`
	// FaasPort     string `json:"faasPort"`
	// FaasPath     string `json:"faasPath"`
	// FaaSUser     string `json:"faasUser"`
	// FaaSPassword string `json:"faasPassword"`
}

type Node struct {
	NodeInfo
	NodeMetadata
	infoChan chan *NodeInfo
}

func (c Catalog) AddAvailableFunctions(functionStatus types.FunctionStatus) {
	for _, fn := range c[selfCatagoryKey].AvailableFunctions {
		if functionStatus.Name == fn.Name {
			return
		}
	}
	functionSet := append(c[selfCatagoryKey].AvailableFunctions, functionStatus)
	c[selfCatagoryKey].AvailableFunctions = functionSet

	publishInfo(c[selfCatagoryKey].infoChan, &c[selfCatagoryKey].NodeInfo)
}

func (c Catalog) ListAvailableFunctions(infoLevel InfoLevel) []types.FunctionStatus {
	var functionStatus []types.FunctionStatus
	functionnameSet := make(map[string]struct{})
	switch infoLevel {
	case LocalLevel:
		for _, fn := range c[selfCatagoryKey].AvailableFunctions {
			if _, exist := functionnameSet[fn.Name]; !exist {
				functionStatus = append(functionStatus, fn)
				functionnameSet[fn.Name] = struct{}{}
			}
		}
	case ClusterLevel:
		for id, node := range c {
			if id == selfCatagoryKey {
				continue
			}
			for _, fn := range node.AvailableFunctions {
				if _, exist := functionnameSet[fn.Name]; !exist {
					functionStatus = append(functionStatus, fn)
					functionnameSet[fn.Name] = struct{}{}
				}
			}
		}
	}
	return functionStatus
}

func (c Catalog) InitAvailableFunctions(fns []types.FunctionStatus) {
	c[selfCatagoryKey].AvailableFunctions = fns

	publishInfo(c[selfCatagoryKey].infoChan, &c[selfCatagoryKey].NodeInfo)
}

// func ListAvailableFunctions(c Catalog) {
// 	for _, fn := range c[selfCatagoryKey].AvailableFunctions {
// 		if functionName == fn {
// 			return
// 		}
// 	}
// 	functionSet := append(c[selfCatagoryKey].AvailableFunctions, functionName)
// 	c[selfCatagoryKey].AvailableFunctions = functionSet

// 	publishInfo(c[selfCatagoryKey].infoChan, &c[selfCatagoryKey].NodeInfo)
// }

func publishInfo(infoChan chan *NodeInfo, info *NodeInfo) {
	infoChan <- info
}
