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
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/p2p/discovery/mdns"
	ma "github.com/multiformats/go-multiaddr"
)

const DiscoveryServiceTag = "faasd-localcluster"
const pubKeyPeerPath = "/opt/p2p/pubKey-peer/"
const mode = "static" //or mdns

// discoveryNotifee gets notified when we find a new peer via mDNS discovery
type discoveryNotifee struct {
	h  host.Host
	ps *pubsub.PubSub
	c  Catalog
}

type faasNotifiee struct {
	h host.Host
}

func setupDiscovery(h host.Host, ps *pubsub.PubSub, c Catalog) error {
	// setup mDNS discovery to find local peers
	switch mode {
	case "static":
		h.Network().Notify(&faasNotifiee{h: h})
		return staticDiscovery(&discoveryNotifee{h: h, ps: ps, c: c})
	case "mdns":
		s := mdns.NewMdnsService(h, DiscoveryServiceTag, &discoveryNotifee{h: h, ps: ps, c: c})
		return s.Start()
	default:
		return fmt.Errorf("discover peer mode %s not found", mode)
	}
}
func staticDiscovery(n *discoveryNotifee) error {
	// return nil
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
		// this is when found peer subjectly, not objectly
		n.HandlePeerFound(pi)
	}
	return nil
}
func extractIP4fromMultiaddr(maddr ma.Multiaddr) string {
	val, err := maddr.ValueForProtocol(ma.P_IP4)
	if err != nil {
		log.Printf("Cannot extract IP address: %s", err)
		return ""
	}

	return val
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
		NodeInfo: NodeInfo{},
		NodeMetadata: NodeMetadata{
			Ip: extractIP4fromMultiaddr(pi.Addrs[0]),
		},
		// do need info chan for the external peer
		infoChan: nil,
	}
	_, subErr := subscribeInfoRoom(ctx, n.ps, infoRoomName, n.h.ID(), n.c)
	if subErr != nil {
		err := fmt.Errorf("error subcribe to info room: %s", err)
		log.Fatal(err)
	}

}

// func InitAvailableFunctions(host host.Host, peerID peer.ID) {
// 	host.NewStream()
// }

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

func (n *faasNotifiee) Listen(network network.Network, maddr ma.Multiaddr) {

}

func (n *faasNotifiee) ListenClose(network network.Network, maddr ma.Multiaddr) {

}

// send the initial available function if the new peer join
func (n *faasNotifiee) Connected(network network.Network, conn network.Conn) {
	// fmt.Println("Peer store:", n.h.Peerstore().Peers())
	// if do not do it concurrently, the peers will block to try new stream at the same time
	go func() {
		fmt.Printf("New Peer Join: %s\n", conn.RemotePeer())
		stream, err := n.h.NewStream(context.Background(), conn.RemotePeer(), faasProtocolID)
		if err != nil {
			log.Fatalf("Failed to open stream: %v", err)
			return
		}
		defer stream.Close()
		// TODO: Send Inital avaialable function
		message := "Hello, specific peer!"
		_, err = stream.Write([]byte(message))
		if err != nil {
			log.Fatalf("Failed to send message: %v", err)
		}
		fmt.Println("Message sent to specific peer")
	}()

}

func (n *faasNotifiee) Disconnected(network network.Network, conn network.Conn) {

}
