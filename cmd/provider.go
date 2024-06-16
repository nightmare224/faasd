package cmd

import (
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"os"
	"path"
	"sync/atomic"
	"time"

	"github.com/containerd/containerd"
	bootstrap "github.com/openfaas/faas-provider"
	"github.com/openfaas/faas-provider/logs"
	"github.com/openfaas/faas-provider/types"
	faasd "github.com/openfaas/faasd/pkg"
	"github.com/openfaas/faasd/pkg/cninetwork"
	faasdlogs "github.com/openfaas/faasd/pkg/logs"
	"github.com/openfaas/faasd/pkg/provider/catalog"
	"github.com/openfaas/faasd/pkg/provider/config"
	"github.com/openfaas/faasd/pkg/provider/handlers"
	promapi "github.com/prometheus/client_golang/api"
	promv1 "github.com/prometheus/client_golang/api/prometheus/v1"
	"github.com/spf13/cobra"
)

const secretDirPermission = 0755

type ExternalHostInfo struct {
	Ip       string `json:"ip"`
	Hostname string `json:"hostname"`
	Port     string `json:"port"`
}

func makeProviderCmd() *cobra.Command {
	var command = &cobra.Command{
		Use:   "provider",
		Short: "Run the faasd-provider",
	}

	command.Flags().String("pull-policy", "Always", `Set to "Always" to force a pull of images upon deployment, or "IfNotPresent" to try to use a cached image.`)

	command.RunE = runProviderE

	return command
}

func runProviderE(cmd *cobra.Command, _ []string) error {

	pullPolicy, flagErr := cmd.Flags().GetString("pull-policy")
	if flagErr != nil {
		return flagErr
	}

	alwaysPull := false
	if pullPolicy == "Always" {
		alwaysPull = true
	}

	config, providerConfig, err := config.ReadFromEnv(types.OsEnv{})
	if err != nil {
		return err
	}

	log.Printf("faasd-provider starting..\tService Timeout: %s\n", config.WriteTimeout.String())
	printVersion()

	wd, err := os.Getwd()
	if err != nil {
		return err
	}

	writeHostsErr := os.WriteFile(path.Join(wd, "hosts"),
		[]byte(`127.0.0.1	localhost`), workingDirectoryPermission)

	if writeHostsErr != nil {
		return fmt.Errorf("cannot write hosts file: %s", writeHostsErr)
	}

	writeResolvErr := os.WriteFile(path.Join(wd, "resolv.conf"),
		[]byte(`nameserver 8.8.8.8`), workingDirectoryPermission)

	if writeResolvErr != nil {
		return fmt.Errorf("cannot write resolv.conf file: %s", writeResolvErr)
	}

	cni, err := cninetwork.InitNetwork()
	if err != nil {
		return err
	}

	client, err := containerd.New(providerConfig.Sock)
	if err != nil {
		return err
	}

	defer client.Close()

	//TODO: need to be better (ex: delete network)
	handlers.ResumeOrDeleteFunctions(client, cni, faasd.DefaultFunctionNamespace)

	invokeResolver := handlers.NewInvokeResolver(client)

	baseUserSecretsPath := path.Join(wd, "secrets")
	if err := moveSecretsToDefaultNamespaceSecrets(
		baseUserSecretsPath,
		faasd.DefaultFunctionNamespace); err != nil {
		return err
	}

	// create the catalog to store p
	c := catalog.NewCatalog()
	node := initSelfCatagory(c, client)
	// create catalog
	InitNetworkErr := catalog.InitInfoNetwork(c)
	if InitNetworkErr != nil {
		return fmt.Errorf("cannot init info network: %s", InitNetworkErr)
	}

	localResolver := faasd.NewLocalResolver(path.Join(faasdwd, "hosts"))
	go localResolver.Start()

	// start the local update
	promClient := initPromClient(localResolver)
	go node.ListenUpdateInfo(client, &promClient)

	// faasP2PMappingList := catalog.NewFaasP2PMappingList(c)

	bootstrapHandlers := types.FaaSHandlers{
		// FunctionProxy: proxy.NewHandlerFunc(*config, invokeResolver, false),
		FunctionProxy:  handlers.MakeTriggerHandler(*config, invokeResolver, c),
		DeleteFunction: handlers.MakeDeleteHandler(client, cni, c),
		DeployFunction: handlers.MakeDeployHandler(client, cni, baseUserSecretsPath, alwaysPull, c),
		FunctionLister: handlers.MakeReadHandler(client, c),
		FunctionStatus: handlers.MakeReplicaReaderHandler(client, c),
		ScaleFunction:  handlers.MakeReplicaUpdateHandler(client, cni, baseUserSecretsPath, c),
		UpdateFunction: handlers.MakeUpdateHandler(client, cni, baseUserSecretsPath, alwaysPull),
		// Health:          func(w http.ResponseWriter, r *http.Request) {},
		Health:          handlers.MakeHealthHandler(node),
		Info:            handlers.MakeInfoHandler(Version, GitCommit),
		ListNamespaces:  handlers.MakeNamespacesLister(client),
		Secrets:         handlers.MakeSecretHandler(client.NamespaceService(), baseUserSecretsPath),
		Logs:            logs.NewLogHandlerFunc(faasdlogs.New(), config.ReadTimeout),
		MutateNamespace: handlers.MakeMutateNamespace(client),
	}

	log.Printf("Listening on: 0.0.0.0:%d\n", *config.TCPPort)
	bootstrap.Serve(cmd.Context(), &bootstrapHandlers, config)
	return nil

}
func initSelfCatagory(c catalog.Catalog, client *containerd.Client) *catalog.Node {
	// init available function to catalog
	fns, err := handlers.ListFunctionStatus(client, faasd.DefaultFunctionNamespace)
	if err != nil {
		fmt.Printf("cannot init available function: %s", err)
		panic(err)
	}

	c.NewNodeCatalogEntry(c.GetSelfCatalogKey(), catalog.GetSelfFaasP2PIp())

	for i, fn := range fns {
		// TODO: should be more sphofisticate
		// if fn.AvailableReplicas != 0 {
		c.FunctionCatalog[fn.Name] = &fns[i]
		c.NodeCatalog[c.GetSelfCatalogKey()].AvailableFunctionsReplicas[fn.Name] = fn.AvailableReplicas
		c.NodeCatalog[c.GetSelfCatalogKey()].FunctionExecutionTime[fn.Name] = new(atomic.Int64)
		c.NodeCatalog[c.GetSelfCatalogKey()].FunctionExecutionTime[fn.Name].Store(1)
		// }
	}

	return c.NodeCatalog[c.GetSelfCatalogKey()]
}

// TODO:Clean up the unsuable container in environement
// func cleanDeadContainer() {

// }

func initPromClient(localResolver faasd.Resolver) promv1.API {
	got := make(chan string, 1)
	go localResolver.Get("prometheus", got, time.Second*5)
	ipAddress := <-got
	close(got)
	promClient, _ := promapi.NewClient(promapi.Config{
		Address: fmt.Sprintf("http://%s:9090", ipAddress),
	})
	promAPIClient := promv1.NewAPI(promClient)

	return promAPIClient
}

/*
* Mutiple namespace support was added after release 0.13.0
* Function will help users to migrate on multiple namespace support of faasd
 */
func moveSecretsToDefaultNamespaceSecrets(baseSecretPath string, defaultNamespace string) error {
	newSecretPath := path.Join(baseSecretPath, defaultNamespace)

	err := ensureSecretsDir(newSecretPath)
	if err != nil {
		return err
	}

	files, err := ioutil.ReadDir(baseSecretPath)
	if err != nil {
		return err
	}

	for _, f := range files {
		if !f.IsDir() {

			newPath := path.Join(newSecretPath, f.Name())

			// A non-nil error means the file wasn't found in the
			// destination path
			if _, err := os.Stat(newPath); err != nil {
				oldPath := path.Join(baseSecretPath, f.Name())

				if err := copyFile(oldPath, newPath); err != nil {
					return err
				}

				log.Printf("[Migration] Copied %s to %s", oldPath, newPath)
			}
		}
	}

	return nil
}

func copyFile(src, dst string) error {
	inputFile, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("opening %s failed %w", src, err)
	}
	defer inputFile.Close()

	outputFile, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_APPEND, secretDirPermission)
	if err != nil {
		return fmt.Errorf("opening %s failed %w", dst, err)
	}
	defer outputFile.Close()

	// Changed from os.Rename due to issue in #201
	if _, err := io.Copy(outputFile, inputFile); err != nil {
		return fmt.Errorf("writing into %s failed %w", outputFile.Name(), err)
	}

	return nil
}
