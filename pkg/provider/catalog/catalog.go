package catalog

type NodeInfo struct {
	AvailableFunctions []string `json:"availableFunctions"`
	Overload           bool     `json:"overload"`
}

// key is the peer ID in string
type Catalog map[string]*Node

type NodeMetadata struct {
	infoChan chan *NodeInfo
}

type Node struct {
	NodeInfo
	NodeMetadata
}
