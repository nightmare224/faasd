package catalog

import (
	"context"
	"fmt"

	libp2p "github.com/libp2p/go-libp2p"
	pubsub "github.com/libp2p/go-libp2p-pubsub"
	"github.com/libp2p/go-libp2p/core/host"
)

var ip string = "0.0.0.0"

func InitInfoNetwork(c Catalog) (chan *NodeInfo, error) {
	ctx := context.Background()
	host := newlibp2pHost()
	ps := newPubSubRouter(ctx, host)
	// discovery the other host and join their room
	setupDiscovery(host, ps, c)

	// create a info room as itself ID
	ir, err := subscribeInfoRoom(ctx, ps, host.ID().String(), host.ID(), c)
	if err != nil {
		return nil, err
	}
	// the info of itself
	c[host.ID().String()] = &Node{
		NodeInfo: NodeInfo{},
		NodeMetadata: NodeMetadata{
			infoChan: ir.infoChan,
		},
	}

	return ir.infoChan, nil
}

func newlibp2pHost() host.Host {
	host, err := libp2p.New(libp2p.ListenAddrStrings(fmt.Sprintf("/ip4/%s/udp/8282/quic-v1", ip)))
	if err != nil {
		panic(err)
	}
	return host
}

// create a new PubSub service using the GossipSub router
func newPubSubRouter(ctx context.Context, host host.Host) *pubsub.PubSub {
	ps, err := pubsub.NewGossipSub(ctx, host)
	if err != nil {
		panic(err)
	}
	return ps
}
