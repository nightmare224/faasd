package catalog

import (
	"context"
	"encoding/json"
	"log"
	"sync/atomic"

	pubsub "github.com/libp2p/go-libp2p-pubsub"
	"github.com/libp2p/go-libp2p/core/peer"
)

type InfoRoom struct {
	infoChan chan *NodeInfo
	ctx      context.Context
	ps       *pubsub.PubSub
	topic    *pubsub.Topic
	sub      *pubsub.Subscription
	// would be the id of owner peer
	infoRoomName string
	// the one that join this room
	selfID peer.ID
}

func (ir *InfoRoom) GetSelfIDString() string {
	return ir.selfID.String()
}

func (ir *InfoRoom) GetInfoChan() chan *NodeInfo {
	return ir.infoChan
}

func subscribeInfoRoom(ctx context.Context, ps *pubsub.PubSub, infoRoomName string, selfID peer.ID, c Catalog) (*InfoRoom, error) {
	// may help propagate the messages but maybe not consuming the messages
	topic, err := ps.Join(infoRoomName)
	if err != nil {
		return nil, err
	}
	// actually receive the messages and process it
	sub, err := topic.Subscribe()
	if err != nil {
		return nil, err
	}

	ir := &InfoRoom{
		// only one buffer space (always one newest info)
		infoChan:     nil,
		ctx:          ctx,
		ps:           ps,
		topic:        topic,
		sub:          sub,
		infoRoomName: infoRoomName,
		selfID:       selfID,
	}
	// subcribe to room
	if infoRoomName != selfID.String() {
		go ir.subscribeLoop(c, infoRoomName)
		log.Printf("Join info room: %s\n", infoRoomName)
	} else { // create a room
		// new the channel to publish the infomation
		ir.infoChan = make(chan *NodeInfo)
		go ir.publishLoop(c)
	}

	return ir, nil
}
func (ir *InfoRoom) publishLoop(c Catalog) {
	for {
		info := <-ir.infoChan
		infoMsg := packNodeInfoMsg(c, info)
		// log.Println("Ready to publish:", info)
		infoBytes, err := json.Marshal(infoMsg)
		if err != nil {
			log.Printf("serialized info message error: %s\n", err)
			continue
		}
		err = ir.topic.Publish(ir.ctx, infoBytes)
		if err != nil {
			log.Printf("publish info message error: %s\n", err)
			continue
		}
	}

}
func (ir *InfoRoom) subscribeLoop(c Catalog, infoRoomName string) {
	for {
		msg, err := ir.sub.Next(ir.ctx)
		if err != nil {
			log.Printf("receive info message error: %s\n", err)
			continue
		}

		infoMsg := new(NodeInfoMsg)
		err = json.Unmarshal(msg.Data, infoMsg)
		if err != nil {
			log.Printf("deserialized info message error: %s\n", err)
			continue
		}

		// update the info in the node
		unpackNodeInfoMsg(c, infoMsg, infoRoomName)
	}
}
func packNodeInfoMsg(c Catalog, info *NodeInfo) *NodeInfoMsg {
	infoMsg := new(NodeInfoMsg)
	// AvailableFunctions
	for fname, availableReplicas := range info.AvailableFunctionsReplicas {
		fn := *c.FunctionCatalog[fname]
		fn.AvailableReplicas = availableReplicas
		infoMsg.AvailableFunctions = append(infoMsg.AvailableFunctions, fn)
	}
	// overload
	infoMsg.Overload = info.Overload

	return infoMsg
}
func unpackNodeInfoMsg(c Catalog, infoMsg *NodeInfoMsg, infoRoomName string) {

	node := c.NodeCatalog[infoRoomName]
	// update pressure
	node.Overload = infoMsg.Overload
	// reocrd available replicate and update functionCatalog
	updateReplicas := make(map[string]uint64)
	for i, fn := range infoMsg.AvailableFunctions {
		// create a new FunctionExecutionTime entry if not exist yet but need now (new replica)
		if _, exist := node.FunctionExecutionTime[fn.Name]; !exist && fn.AvailableReplicas > 0 {
			node.FunctionExecutionTime[fn.Name] = new(atomic.Int64)
			node.FunctionExecutionTime[fn.Name].Store(1)
		}
		// reset the execTime to give a new change when there are new add replica
		if replica, exist := node.AvailableFunctionsReplicas[fn.Name]; (!exist && fn.AvailableReplicas > 0) || (exist && replica < fn.AvailableReplicas) {
			node.FunctionExecutionTime[fn.Name].Store(1)
		}
		// add to function Catalog if it is new function
		if _, exist := c.FunctionCatalog[fn.Name]; !exist {
			c.FunctionCatalog[fn.Name] = &infoMsg.AvailableFunctions[i]
		}

		updateReplicas[fn.Name] = fn.AvailableReplicas
	}

	node.AvailableFunctionsReplicas = updateReplicas

	// update global replica
	c.updatetReplicas()
}
