package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"net/url"
	"os"
	"path"

	"github.com/containerd/containerd"
	bootstrap "github.com/openfaas/faas-provider"
	"github.com/openfaas/faas-provider/auth"
	"github.com/openfaas/faas-provider/logs"
	"github.com/openfaas/faas-provider/proxy"
	"github.com/openfaas/faas-provider/types"
	faasd "github.com/openfaas/faasd/pkg"
	"github.com/openfaas/faasd/pkg/cninetwork"
	faasdlogs "github.com/openfaas/faasd/pkg/logs"
	"github.com/openfaas/faasd/pkg/provider/config"
	"github.com/openfaas/faasd/pkg/provider/handlers"
	"github.com/openfaas/go-sdk"
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

func connectExternalProvider() ([]*sdk.Client, error) {
	externalSecretMountPath := path.Join(faasdwd, "secrets/external")
	if err := ensureWorkingDir(externalSecretMountPath); err != nil {
		return nil, err
	}

	var clients []*sdk.Client = nil
	files, _ := os.ReadDir(externalSecretMountPath)
	hostsfile, err := os.OpenFile("/etc/hosts", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		log.Fatal(err)
		return nil, err
	}
	defer hostsfile.Close()
	for _, file := range files {
		if !file.IsDir() {
			continue
		}
		secretPath := path.Join(externalSecretMountPath, file.Name())
		reader := auth.ReadBasicAuthFromDisk{
			SecretMountPath: secretPath,
		}
		credentials, err := reader.Read()
		if err != nil {
			log.Fatal(err)
			continue
		}
		data, hostErr := os.ReadFile(path.Join(secretPath, "basic-auth-host"))
		hostinfo := ExternalHostInfo{}
		if hostErr != nil {
			err := fmt.Errorf("unable to load %s", hostErr)
			log.Fatal(err)
			continue
		}
		json.Unmarshal(data, &hostinfo)
		hosturl := fmt.Sprintf("http://%s:%s", hostinfo.Ip, hostinfo.Port)
		if hostinfo.Hostname != "" {
			_, err := hostsfile.WriteString(fmt.Sprintf("%s\t%s", hostinfo.Ip, hostinfo.Hostname))
			if err != nil {
				err := fmt.Errorf("cannot write hosts file: %s", err)
				log.Fatal(err)
				continue
			}
			hosturl = fmt.Sprintf("http://%s:%s", hostinfo.Hostname, hostinfo.Port)
		}
		// writeHostsErr := os.WriteFile("/etc/hosts", []byte(fmt.Sprintf("%s\t%s", hostinfo.Ip, hostinfo.Hostname)), 0x0644)

		gatewayURL, _ := url.Parse(hosturl)
		auth := &sdk.BasicAuth{
			Username: credentials.User,
			Password: credentials.Password,
		}
		client := sdk.NewClient(gatewayURL, auth, http.DefaultClient)
		clients = append(clients, client)
		log.Printf("Connect with the external client: %s\n", hosturl)
	}

	return clients, nil
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

	externalClients, err := connectExternalProvider()

	invokeResolver := handlers.NewInvokeResolver(client)

	baseUserSecretsPath := path.Join(wd, "secrets")
	if err := moveSecretsToDefaultNamespaceSecrets(
		baseUserSecretsPath,
		faasd.DefaultFunctionNamespace); err != nil {
		return err
	}

	bootstrapHandlers := types.FaaSHandlers{
		FunctionProxy:   proxy.NewHandlerFunc(*config, invokeResolver, false),
		DeleteFunction:  handlers.MakeDeleteHandler(client, cni),
		DeployFunction:  handlers.MakeDeployHandler(client, cni, baseUserSecretsPath, alwaysPull),
		FunctionLister:  handlers.MakeReadHandler(client, externalClients),
		FunctionStatus:  handlers.MakeReplicaReaderHandler(client),
		ScaleFunction:   handlers.MakeReplicaUpdateHandler(client, cni),
		UpdateFunction:  handlers.MakeUpdateHandler(client, cni, baseUserSecretsPath, alwaysPull),
		Health:          func(w http.ResponseWriter, r *http.Request) {},
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
