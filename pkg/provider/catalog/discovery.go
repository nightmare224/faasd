package catalog

import (
	"context"
	"fmt"
	"log"
	"os"
	"path"

	pubsub "github.com/libp2p/go-libp2p-pubsub"
	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/p2p/discovery/mdns"
	ma "github.com/multiformats/go-multiaddr"
)

const DiscoveryServiceTag = "faasd-localcluster"
const pubKeyPeerPath = "/tmp/faasd-p2p/pubKey-peer/"

// discoveryNotifee gets notified when we find a new peer via mDNS discovery
type discoveryNotifee struct {
	h  host.Host
	ps *pubsub.PubSub
	c  Catalog
}

const mode = "static" //or mdns

func setupDiscovery(h host.Host, ps *pubsub.PubSub, c Catalog) error {
	// setup mDNS discovery to find local peers
	switch mode {
	case "static":
		return staticDiscovery(&discoveryNotifee{h: h, ps: ps, c: c})
	case "mdns":
		s := mdns.NewMdnsService(h, DiscoveryServiceTag, &discoveryNotifee{h: h, ps: ps, c: c})
		return s.Start()
	default:
		return fmt.Errorf("discover peer mode %s not found", mode)
	}
}
func staticDiscovery(n *discoveryNotifee) error {

	dir, _ := os.ReadDir(pubKeyPeerPath)
	for _, entry := range dir {
		// filename is ip
		peerIP := entry.Name()
		peerID := readIDFromPubKey(path.Join(pubKeyPeerPath, peerIP))
		// skip when found itself
		if peerID == n.h.ID() {
			continue
		}
		maddr, err := ma.NewMultiaddr(fmt.Sprintf("/ip4/%s/udp/%s/quic-v1", peerIP, port))
		if err != nil {
			log.Println(err)
			return err
		}
		pi := peer.AddrInfo{
			ID:    peerID,
			Addrs: []ma.Multiaddr{maddr},
		}
		n.HandlePeerFound(pi)
	}
	return nil
}

func (n *discoveryNotifee) HandlePeerFound(pi peer.AddrInfo) {
	log.Printf("Discovered new peer %s\n", pi.ID)
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

func readIDFromPubKey(filepath string) peer.ID {
	pubKeyData, err := os.ReadFile(filepath)
	if err != nil {
		log.Printf("Failed to read public key file: %s", err)
		panic(err)
	}
	key, err := crypto.UnmarshalPublicKey(pubKeyData)
	if err != nil {
		log.Printf("Failed to extract key from message key data: %s", err)
		panic(err)
	}
	idFromKey, err := peer.IDFromPublicKey(key)
	if err != nil {
		log.Printf("Failed to extract ID from private key: %s", err)
		panic(err)
	}

	return idFromKey
}
