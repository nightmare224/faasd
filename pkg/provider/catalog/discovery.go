package catalog

import (
	"context"
	"fmt"
	"log"

	pubsub "github.com/libp2p/go-libp2p-pubsub"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/p2p/discovery/mdns"
)

const DiscoveryServiceTag = "faasd-localcluster"

// discoveryNotifee gets notified when we find a new peer via mDNS discovery
type discoveryNotifee struct {
	h  host.Host
	ps *pubsub.PubSub
	c  Catalog
}

func setupDiscovery(h host.Host, ps *pubsub.PubSub, c Catalog) error {
	// setup mDNS discovery to find local peers
	s := mdns.NewMdnsService(h, DiscoveryServiceTag, &discoveryNotifee{h: h, ps: ps, c: c})
	return s.Start()
}
func (n *discoveryNotifee) HandlePeerFound(pi peer.AddrInfo) {
	log.Printf("discovered new peer %s\n", pi.ID)
	ctx := context.Background()
	err := n.h.Connect(ctx, pi)
	if err != nil {
		err := fmt.Errorf("error connecting to peer %s", err)
		log.Fatal(err)
	}
	infoRoomName := pi.ID.String()
	// init the catagory for the find peer
	n.c[infoRoomName] = &Node{
		NodeInfo:     NodeInfo{},
		NodeMetadata: NodeMetadata{},
		// do need info chan for the external peer
		infoChan: nil,
	}
	_, subErr := subscribeInfoRoom(ctx, n.ps, infoRoomName, n.h.ID(), n.c)
	if subErr != nil {
		err := fmt.Errorf("error subcribe to info room: %s", err)
		log.Fatal(err)
	}

}
