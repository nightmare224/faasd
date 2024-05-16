package catalog

import (
	"context"
	"fmt"
	"log"
	"os"

	libp2p "github.com/libp2p/go-libp2p"
	pubsub "github.com/libp2p/go-libp2p-pubsub"
	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/host"
)

const ip = "10.211.55.24"
const port = "8282"

// const pubKeySelf = "/tmp/faasd-p2p/pubKey"
const privKeySelf = "/opt/p2p/privKey"

func InitInfoNetwork(c Catalog) (chan *NodeInfo, error) {
	ctx := context.Background()
	host := newlibp2pHost()
	ps := newPubSubRouter(ctx, host)
	// discovery the other host and join their room
	err := setupDiscovery(host, ps, c)
	if err != nil {
		return nil, err
	}

	// create a info room as itself ID
	ir, err := subscribeInfoRoom(ctx, ps, host.ID().String(), host.ID(), c)
	if err != nil {
		return nil, err
	}
	// the info of itself
	c[selfCatagoryKey] = &Node{
		NodeInfo: NodeInfo{},
		NodeMetadata: NodeMetadata{
			Ip: extractIP4fromMultiaddr(host.Addrs()[0]),
		},
		infoChan: ir.infoChan,
	}

	return ir.infoChan, nil
}

func newlibp2pHost() host.Host {

	privKeyData, err := os.ReadFile(privKeySelf)
	if err != nil {
		log.Printf("Failed to read private key file: %s", err)
		panic(err)
	}
	privKey, err := crypto.UnmarshalPrivateKey(privKeyData)
	if err != nil {
		log.Printf("Failed to extract key from message key data: %s", err)
		panic(err)
	}
	multiaddr := fmt.Sprintf("/ip4/%s/udp/%s/quic-v1", ip, port)
	host, err := libp2p.New(
		libp2p.ListenAddrStrings(multiaddr),
		libp2p.Identity(privKey),
	)
	if err != nil {
		log.Printf("Failed to create new host: %s", err)
		panic(err)
	}
	log.Printf("Created new p2p host %s\n", host.ID().String())

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
