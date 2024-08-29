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
			P2PID:  GetSelfCatalogKey(),
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

func (c Catalog) RankNodeByRTT() {

	// TODO: Can make this run periodically
	RTTs := make([]time.Duration, 0)
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
	}
	slices.Sort(RTTs)

	// make the length fit with the number of node
	*c.SortedP2PID = (*c.SortedP2PID)[:len(RTTs)]
	// copy back to original array
	for i, rtt := range RTTs {
		(*c.SortedP2PID)[i] = RTTtoP2PID[rtt]
	}
}
