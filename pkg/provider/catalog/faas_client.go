package catalog

import (
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"slices"
	"time"

	"github.com/openfaas/go-sdk"
	probing "github.com/prometheus-community/pro-bing"
)

// type FaasP2PMapping struct {
// 	FaasClient *sdk.Client
// 	P2PID      string
// }

type FaasClient struct {
	Client *sdk.Client
	// for convenient
	P2PID string
}

const faasConfigPath = "/opt/faasd/secrets/faasclient/"

type faasConfig struct {
	Ip       string `json:"ip"`
	Port     string `json:"port"`
	Path     string `json:"path"`
	User     string `json:"user"`
	Password string `json:"password"`
}

func NewFaasClientWithIp(ip string, p2pid string) FaasClient {
	if ip == GetSelfFaasP2PIp() {
		return FaasClient{
			Client: nil,
			P2PID:  GetSelfFaasP2PIp(),
		}
	}

	faasConfigData, err := os.ReadFile(path.Join(faasConfigPath, ip))
	if err != nil {
		log.Printf("Error reading JSON file: %s\n", err)
		return FaasClient{}
	}

	var faasConfig faasConfig
	err = json.Unmarshal(faasConfigData, &faasConfig)
	if err != nil {
		log.Printf("Failed to extract kubeconfig from json file: %s", err)
		return FaasClient{}
	}
	// fmt.Println("faasConfig:", faasConfig)
	hosturl := fmt.Sprintf("http://%s:%s", faasConfig.Ip, faasConfig.Port)

	gatewayURL, _ := url.Parse(hosturl)
	auth := &sdk.BasicAuth{
		Username: faasConfig.User,
		Password: faasConfig.Password,
	}
	client := sdk.NewClient(gatewayURL, auth, http.DefaultClient)
	log.Printf("read faas client: %v\n", client)

	return FaasClient{
		Client: client,
		P2PID:  p2pid,
	}
}

func (f FaasClient) Resolve(name string) (url.URL, error) {

	return *f.Client.GatewayURL, nil
}

// TODO: need to trigger this when there is new Node into Catalog
// sort the P2P ID from the fastest to slowest
func (c Catalog) RankNodeByRTT() {

	// TODO: make this run periodically?
	var RTTs []time.Duration
	RTTtoP2PID := make(map[time.Duration]string)
	for p2pID, p2pNode := range c.NodeCatalog {
		// for itself it is 0
		rtt := time.Duration(0)
		if p2pID != selfCatagoryKey {
			ip, _, _ := net.SplitHostPort(p2pNode.Client.GatewayURL.Host)
			pinger, err := probing.NewPinger(ip)
			if err != nil {
				panic(err)
			}
			pinger.Count = 3
			err = pinger.Run() // Blocks until finished.
			if err != nil {
				panic(err)
			}
			stats := pinger.Statistics()
			rtt = stats.AvgRtt
		}
		RTTtoP2PID[rtt] = p2pID
		RTTs = append(RTTs, rtt)
		fmt.Println("RTT: ", rtt, "P2P ID: ", p2pID)
	}
	slices.Sort(RTTs)

	// make the length fit with the number of node
	c.SortedP2PID = c.SortedP2PID[:len(RTTs)]
	// copy back to original array
	for i, rtt := range RTTs {
		c.SortedP2PID[i] = RTTtoP2PID[rtt]
	}

}

func totalAmountFaasClient() int {
	dir, _ := os.ReadDir(pubKeyPeerPath)

	return len(dir)
}

// func (mapping *FaasP2PMapping) Resolve(name string) (url.URL, error) {
// 	// mapping.FaasClient.GatewayURL
// 	// functionAddr := url.URL{
// 	// 	Scheme: "http",
// 	// 	Host:   fmt.Sprintf("%s:%s", config.Ip, config.Port),
// 	// }

// 	return *mapping.FaasClient.GatewayURL, nil
// }

// func (l FaasP2PMappingList) GetByP2PID(p2pID string) FaasP2PMapping {
// 	for _, mapping := range l {
// 		if mapping.P2PID == p2pID {
// 			return mapping
// 		}
// 	}

// 	return FaasP2PMapping{}
// }

// func NewFaasP2PMappingList(c Catalog) FaasP2PMappingList {

// 	faasP2PMappingList := FaasP2PMappingList{
// 		// also add itself into it (for itself, it don't need the faas client)
// 		{
// 			FaasClient: nil,
// 			P2PID:      selfCatagoryKey,
// 		},
// 	}
// 	// TODO: this is work around, to prevent the nodecatalog not yet init
// 	time.Sleep(5 * time.Second)
// 	faasClients := newFaasClients(c.NodeCatalog[selfCatagoryKey].Ip)
// 	log.Printf("total faas clients: %v\n", faasClients)

// 	for _, client := range faasClients {
// 		for p2pID, p2pNode := range c.NodeCatalog {
// 			if strings.HasPrefix(client.GatewayURL.Host, p2pNode.Ip) {
// 				mapping := FaasP2PMapping{
// 					FaasClient: client,
// 					P2PID:      p2pID,
// 				}
// 				faasP2PMappingList = append(faasP2PMappingList, mapping)
// 				break
// 			}
// 		}

// 		// testFaasClient(client)
// 	}

// 	go func() {
// 		rankClientsByRTT(faasP2PMappingList)
// 		node := <-c.nodeChan
// 		for _, client := range faasClients {
// 			if strings.HasPrefix(client.GatewayURL.Host, node.Ip) {
// 				mapping := FaasP2PMapping{
// 					FaasClient: client,
// 					P2PID:      "",
// 				}
// 				faasP2PMappingList = append(faasP2PMappingList, mapping)
// 				break
// 			}
// 		}
// 	}()

// 	// log.Println(faasP2PMappingList)
// 	return faasP2PMappingList
// }

// put it from the fastest client to slowest client
// func rankClientsByRTT(faasP2PMappingList FaasP2PMappingList) {

// 	// TODO: make this run periodically?
// 	var RTTs []time.Duration
// 	RTTtoMapping := make(map[time.Duration]FaasP2PMapping)
// 	for _, mapping := range faasP2PMappingList {
// 		// for itself it is 0
// 		rtt := time.Duration(0)
// 		if mapping.P2PID != selfCatagoryKey {
// 			ip, _, _ := net.SplitHostPort(mapping.FaasClient.GatewayURL.Host)
// 			pinger, err := probing.NewPinger(ip)
// 			if err != nil {
// 				panic(err)
// 			}
// 			pinger.Count = 3
// 			err = pinger.Run() // Blocks until finished.
// 			if err != nil {
// 				panic(err)
// 			}
// 			stats := pinger.Statistics()
// 			rtt = stats.AvgRtt
// 		}
// 		RTTtoMapping[rtt] = mapping
// 		RTTs = append(RTTs, rtt)
// 		fmt.Println("RTT: ", rtt, "P2P ID: ", mapping.P2PID)
// 	}
// 	slices.Sort(RTTs)
// 	// copy back to original array
// 	for i, rtt := range RTTs {
// 		faasP2PMappingList[i] = RTTtoMapping[rtt]
// 	}

// }

// func newFaasClients(selfIP string) []*sdk.Client {
// 	var faasClients []*sdk.Client
// 	dir, _ := os.ReadDir(pubKeyPeerPath)
// 	for _, entry := range dir {
// 		filename := entry.Name()
// 		// skip itself config
// 		if filename == selfIP {
// 			continue
// 		}
// 		faasConfigData, err := os.ReadFile(path.Join(faasConfigPath, filename))
// 		if err != nil {
// 			log.Printf("Error reading JSON file: %s\n", err)
// 			continue
// 		}

// 		var faasConfig FaasConfig
// 		err = json.Unmarshal(faasConfigData, &faasConfig)
// 		if err != nil {
// 			log.Printf("Failed to extract kubeconfig from json file: %s", err)
// 			continue
// 		}
// 		// fmt.Println("faasConfig:", faasConfig)
// 		hosturl := fmt.Sprintf("http://%s:%s", faasConfig.Ip, faasConfig.Port)

// 		gatewayURL, _ := url.Parse(hosturl)
// 		auth := &sdk.BasicAuth{
// 			Username: faasConfig.User,
// 			Password: faasConfig.Password,
// 		}
// 		client := sdk.NewClient(gatewayURL, auth, http.DefaultClient)
// 		faasClients = append(faasClients, client)
// 	}
// 	log.Printf("read faas clients: %v\n", faasClients)

// 	return faasClients
// }
