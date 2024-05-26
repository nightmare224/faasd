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

const infoUpdateIntervalSec = 10

const (
	// CPU average overload threshold within one minitues
	CPUOverloadThreshold = 0.80
	// Memory average overload threshold within one minitues
	MemOverloadThreshold = 0.80
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

func (c Catalog) GetSelfCatalogKey() string {
	return selfCatagoryKey
}

// func (c Catalog) InitAvailableFunctions(fns []types.FunctionStatus) {
// 	c[selfCatagoryKey].AvailableFunctions = fns

// publishInfo(c[selfCatagoryKey].infoChan, &c[selfCatagoryKey].NodeInfo)
// }
