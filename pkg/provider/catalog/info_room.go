package catalog

import (
	"context"
	"encoding/json"
	"fmt"
	"log"

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
		go ir.subscribeLoop(c[infoRoomName])
		log.Printf("Info room join: %s\n", sub.Topic())
	} else { // create a room
		// new the channel to publish the infomation
		ir.infoChan = make(chan *NodeInfo)
		go ir.publishLoop()
		log.Printf("Info room create: %s\n", sub.Topic())
	}

	return ir, nil
}
func (ir *InfoRoom) publishLoop() {
	for {
		info := <-ir.infoChan
		infoBytes, err := json.Marshal(info)
		if err != nil {
			continue
		}
		err = ir.topic.Publish(ir.ctx, infoBytes)
		if err != nil {
			continue
		}
	}

}
func (ir *InfoRoom) subscribeLoop(node *Node) {
	for {
		msg, err := ir.sub.Next(ir.ctx)
		if err != nil {
			// close(ir.Messages)
			return
		}
		// only forward messages delivered by others
		if msg.ReceivedFrom == ir.selfID {
			continue
		}

		info := new(NodeInfo)
		err = json.Unmarshal(msg.Data, info)
		if err != nil {
			continue
		}
		fmt.Println("Receive info:", info)
		// 	// send valid messages onto the Messages channel
		// 	cr.Messages <- cm
		// c[ir.selfID.String()] = info

		// update the info in the node
		node.NodeInfo = *info
	}
}
