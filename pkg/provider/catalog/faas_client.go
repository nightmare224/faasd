package catalog

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"path"
	"strings"

	"github.com/openfaas/go-sdk"
)

type FaasP2PMapping struct {
	FaasClient *sdk.Client
	P2PID      string
}

// type FaasClient sdk.Client

const faasConfigPath = "/opt/faasd/secrets/faasclient/"

type FaasConfig struct {
	Ip       string `json:"ip"`
	Port     string `json:"port"`
	Path     string `json:"path"`
	User     string `json:"user"`
	Password string `json:"password"`
}

func (mapping FaasP2PMapping) Resolve(name string) (url.URL, error) {
	// mapping.FaasClient.GatewayURL
	// functionAddr := url.URL{
	// 	Scheme: "http",
	// 	Host:   fmt.Sprintf("%s:%s", config.Ip, config.Port),
	// }

	return *mapping.FaasClient.GatewayURL, nil
}

func NewFaasP2PMappingList(c Catalog) []FaasP2PMapping {

	faasClients := newFaasClients(c.NodeCatalog[selfCatagoryKey].Ip)
	var faasP2PMappingList []FaasP2PMapping
	for _, client := range faasClients {
		for p2pID, p2pNode := range c.NodeCatalog {
			if strings.HasPrefix(client.GatewayURL.Host, p2pNode.Ip) {
				mapping := FaasP2PMapping{
					FaasClient: client,
					P2PID:      p2pID,
				}
				faasP2PMappingList = append(faasP2PMappingList, mapping)
				break
			}
		}

		// testFaasClient(client)
	}
	// also add itself into it
	mapping := FaasP2PMapping{
		FaasClient: nil,
		P2PID:      selfCatagoryKey,
	}
	faasP2PMappingList = append(faasP2PMappingList, mapping)
	// log.Println(faasP2PMappingList)
	return faasP2PMappingList
}

// func testFaasClient(client *sdk.Client) {
// 	fns, err := client.GetFunctions(context.Background(), "openfaas-fn")
// 	if err != nil {
// 		log.Printf("test error: %s", err)
// 		return
// 	}
// 	log.Println(fns)
// }

func newFaasClients(selfIP string) []*sdk.Client {
	var faasClients []*sdk.Client
	dir, _ := os.ReadDir(pubKeyPeerPath)
	for _, entry := range dir {
		filename := entry.Name()
		// skip itself config
		if filename == selfIP {
			continue
		}
		faasConfigData, err := os.ReadFile(path.Join(faasConfigPath, filename))
		if err != nil {
			log.Printf("Error reading JSON file: %s\n", err)
			continue
		}

		var faasConfig FaasConfig
		err = json.Unmarshal(faasConfigData, &faasConfig)
		if err != nil {
			log.Printf("Failed to extract kubeconfig from json file: %s", err)
			continue
		}
		// fmt.Println("faasConfig:", faasConfig)
		hosturl := fmt.Sprintf("http://%s:%s", faasConfig.Ip, faasConfig.Port)

		gatewayURL, _ := url.Parse(hosturl)
		auth := &sdk.BasicAuth{
			Username: faasConfig.User,
			Password: faasConfig.Password,
		}
		client := sdk.NewClient(gatewayURL, auth, http.DefaultClient)
		faasClients = append(faasClients, client)
	}
	// jsonData, err := os.ReadFile("data.json") // Go 1.16+ simplifies file reading

	return faasClients
}
