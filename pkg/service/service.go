package service

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/containerd/containerd"
	"github.com/containerd/containerd/cio"
	"github.com/containerd/containerd/errdefs"
	"github.com/containerd/containerd/remotes"
	"github.com/containerd/containerd/remotes/docker"
	gocni "github.com/containerd/go-cni"
	"github.com/docker/cli/cli/config"
	"github.com/docker/cli/cli/config/configfile"
	"github.com/openfaas/faasd/pkg/cninetwork"
	"github.com/pkg/errors"
	"golang.org/x/sys/unix"
)

// dockerConfigDir contains "config.json"
const dockerConfigDir = "/var/lib/faasd/.docker/"

func GetTaskIP(ctx context.Context, client *containerd.Client, cni gocni.CNI, name string) (string, error) {
	ctr, ctrErr := client.LoadContainer(ctx, name)
	if ctrErr != nil {
		err := fmt.Errorf("cannot load service %s, error: %s", name, ctrErr)
		return "", err
	}
	task, taskErr := ctr.Task(ctx, nil)
	if taskErr != nil {
		err := fmt.Errorf("cannot load task for service %s, error: %s, create it again", name, taskErr)
		return "", err
	}
	ip, err := cninetwork.GetIPAddress(name, task.Pid())
	if err != nil {
		return "", err
	}
	return ip, nil
}

// stop and delete the task and create it again if need, and resume if it is paused
func EnsureTaskRunning(ctx context.Context, client *containerd.Client, cni gocni.CNI, name string) error {
	ctr, ctrErr := client.LoadContainer(ctx, name)
	if ctrErr != nil {
		err := fmt.Errorf("cannot load service %s, error: %s", name, ctrErr)
		return err
	}

	task, taskErr := ctr.Task(ctx, nil)
	if taskErr != nil {
		log.Printf("cannot load task for service %s, error: %s, create it again", name, taskErr)
		if err := CreateTask(ctx, ctr, cni); err != nil {
			log.Printf("error deploying %s, error: %s\n", name, err)
			return err
		}
	} else {
		status, statusErr := task.Status(ctx)
		if statusErr != nil {
			log.Printf("cannot load task status for %s, error: %s, crate it again", name, statusErr)
			if err := CreateTask(ctx, ctr, cni); err != nil {
				log.Printf("error deploying %s, error: %s\n", name, err)
				return err
			}
		}
		switch status.Status {
		case containerd.Running:
			if _, err := cninetwork.GetIPAddress(name, task.Pid()); err != nil {
				labels := map[string]string{}
				if _, err := cninetwork.CreateCNINetwork(ctx, cni, task, labels); err != nil {
					return err
				}
			}
		case containerd.Stopped, containerd.Unknown:
			if sigErr := task.Kill(ctx, syscall.SIGKILL); sigErr != nil {
				log.Printf("error send SIGKILL to task %s, error: %s\n", name, sigErr)
			}
			if _, delErr := task.Delete(ctx); delErr != nil {
				log.Printf("error deleting stopped task %s, error: %s\n", name, delErr)
			}
			deployErr := CreateTask(ctx, ctr, cni)
			if deployErr != nil {
				log.Printf("error deploying %s, error: %s\n", name, deployErr)
				return deployErr
			}
		case containerd.Paused:
			if resumeErr := task.Resume(ctx); resumeErr != nil {
				log.Printf("error resuming task %s, error: %s\n", name, resumeErr)
				return resumeErr
			}
		}
	}

	return nil
}

// Return the task that is in running state
func GetTaskRunning(ctx context.Context, client *containerd.Client, cni gocni.CNI) ([]string, error) {
	containers, err := client.Containers(ctx)
	if err != nil {
		log.Print("cannot list function\n")
		return []string{}, err
	}

	tasks := []string{}
	for _, c := range containers {
		name := c.ID()
		ctr, ctrErr := client.LoadContainer(ctx, name)
		if ctrErr != nil {
			err := fmt.Errorf("cannot load service %s, error: %s", name, ctrErr)
			return []string{}, err
		}

		task, taskErr := ctr.Task(ctx, nil)
		if taskErr != nil {
			log.Printf("cannot load task for service %s, error: %s, create it again", name, taskErr)
			if err := CreateTask(ctx, ctr, cni); err != nil {
				log.Printf("error deploying %s, error: %s\n", name, err)
				return []string{}, err
			}
		} else {
			status, statusErr := task.Status(ctx)
			if statusErr != nil {
				log.Printf("cannot load task status for %s, error: %s, crate it again", name, statusErr)
				if err := CreateTask(ctx, ctr, cni); err != nil {
					log.Printf("error deploying %s, error: %s\n", name, err)
					return []string{}, err
				}
			}
			if status.Status == containerd.Running {
				tasks = append(tasks, name)
			}
		}
	}

	return tasks, nil
}

func EnsureAllStoppedTaskDelete(ctx context.Context, client *containerd.Client, cni gocni.CNI) error {
	containers, err := client.Containers(ctx)
	if err != nil {
		log.Print("cannot list function\n")
		return err
	}

	for _, c := range containers {
		name := c.ID()
		ctr, ctrErr := client.LoadContainer(ctx, name)
		if ctrErr != nil {
			err := fmt.Errorf("cannot load service %s, error: %s", name, ctrErr)
			return err
		}

		task, taskErr := ctr.Task(ctx, nil)
		if taskErr != nil {
			log.Printf("cannot load task for service %s, error: %s, create it again", name, taskErr)
			if err := CreateTask(ctx, ctr, cni); err != nil {
				log.Printf("error deploying %s, error: %s\n", name, err)
				return err
			}
		} else {
			status, statusErr := task.Status(ctx)
			if statusErr != nil {
				log.Printf("cannot load task status for %s, error: %s, crate it again", name, statusErr)
				if err := CreateTask(ctx, ctr, cni); err != nil {
					log.Printf("error deploying %s, error: %s\n", name, err)
					continue
				}
			}
			if status.Status == containerd.Stopped {
				err = cninetwork.DeleteCNINetwork(ctx, cni, client, name)
				if err != nil {
					log.Printf("[Delete] error removing CNI network for %s, %s\n", name, err)
				}

				if err := Remove(ctx, client, name); err != nil {
					log.Printf("[Delete] error removing %s, %s\n", name, err)
				}
			}
		}
	}

	return nil
}

func EnsureAllTaskRunning(ctx context.Context, client *containerd.Client, cni gocni.CNI) error {
	containers, err := client.Containers(ctx)
	if err != nil {
		log.Print("cannot list function\n")
		return err
	}
	for _, c := range containers {
		name := c.ID()
		err := EnsureTaskRunning(ctx, client, cni, name)
		if err != nil {
			return err
		}
	}

	return nil
}

func CreateTask(ctx context.Context, container containerd.Container, cni gocni.CNI) error {

	name := container.ID()

	task, taskErr := container.NewTask(ctx, cio.BinaryIO("/usr/local/bin/faasd", nil))

	if taskErr != nil {
		return fmt.Errorf("unable to start task: %s, error: %w", name, taskErr)
	}

	log.Printf("Container ID: %s\tTask ID %s:\tTask PID: %d\t\n", name, task.ID(), task.Pid())

	labels := map[string]string{}
	_, err := cninetwork.CreateCNINetwork(ctx, cni, task, labels)

	if err != nil {
		return err
	}

	ip, err := cninetwork.GetIPAddress(name, task.Pid())
	if err != nil {
		return err
	}

	log.Printf("%s has IP: %s.\n", name, ip)

	_, waitErr := task.Wait(ctx)
	if waitErr != nil {
		return errors.Wrapf(waitErr, "Unable to wait for task to start: %s", name)
	}

	if startErr := task.Start(ctx); startErr != nil {
		return errors.Wrapf(startErr, "Unable to start task: %s", name)
	}
	return nil
}

// Remove removes a container
func Remove(ctx context.Context, client *containerd.Client, name string) error {

	container, containerErr := client.LoadContainer(ctx, name)

	if containerErr == nil {
		taskFound := true
		t, err := container.Task(ctx, nil)
		if err != nil {
			if errdefs.IsNotFound(err) {
				taskFound = false
			} else {
				return fmt.Errorf("unable to get task %w: ", err)
			}
		}

		if taskFound {
			status, err := t.Status(ctx)
			if err != nil {
				log.Printf("Unable to get status for: %s, error: %s", name, err.Error())
			} else {
				log.Printf("Status of %s is: %s\n", name, status.Status)
			}

			log.Printf("Need to kill task: %s\n", name)
			if err = killTask(ctx, t); err != nil {
				return fmt.Errorf("error killing task %s, %s, %w", container.ID(), name, err)
			}
		}

		if err := container.Delete(ctx, containerd.WithSnapshotCleanup); err != nil {
			return fmt.Errorf("error deleting container %s, %s, %w", container.ID(), name, err)
		}

	} else {
		service := client.SnapshotService("")
		key := name + "-snapshot"
		if _, err := client.SnapshotService("").Stat(ctx, key); err == nil {
			service.Remove(ctx, key)
		}
	}
	return nil
}

// Adapted from Stellar - https://github.com/stellar
func killTask(ctx context.Context, task containerd.Task) error {

	killTimeout := 30 * time.Second

	wg := &sync.WaitGroup{}
	wg.Add(1)
	var err error

	go func() {
		defer wg.Done()
		if task != nil {
			wait, err := task.Wait(ctx)
			if err != nil {
				log.Printf("error waiting on task: %s", err)
				return
			}

			if err := task.Kill(ctx, unix.SIGTERM, containerd.WithKillAll); err != nil {
				log.Printf("error killing container task: %s", err)
			}

			select {
			case <-wait:
				task.Delete(ctx)
				return
			case <-time.After(killTimeout):
				if err := task.Kill(ctx, unix.SIGKILL, containerd.WithKillAll); err != nil {
					log.Printf("error force killing container task: %s", err)
				}
				return
			}
		}
	}()
	wg.Wait()

	return err
}

func getResolver(ctx context.Context, configFile *configfile.ConfigFile) (remotes.Resolver, error) {
	// credsFunc is based on https://github.com/moby/buildkit/blob/0b130cca040246d2ddf55117eeff34f546417e40/session/auth/authprovider/authprovider.go#L35
	credFunc := func(host string) (string, string, error) {
		if host == "registry-1.docker.io" {
			host = "https://index.docker.io/v1/"
		}
		ac, err := configFile.GetAuthConfig(host)
		if err != nil {
			return "", "", err
		}
		if ac.IdentityToken != "" {
			return "", ac.IdentityToken, nil
		}
		return ac.Username, ac.Password, nil
	}

	authOpts := []docker.AuthorizerOpt{docker.WithAuthCreds(credFunc)}
	authorizer := docker.NewDockerAuthorizer(authOpts...)
	opts := docker.ResolverOptions{
		Hosts: docker.ConfigureDefaultRegistries(docker.WithAuthorizer(authorizer)),
	}
	return docker.NewResolver(opts), nil
}

func PrepareImage(ctx context.Context, client *containerd.Client, imageName, snapshotter string, pullAlways bool) (containerd.Image, error) {
	var (
		empty    containerd.Image
		resolver remotes.Resolver
	)

	if _, statErr := os.Stat(filepath.Join(dockerConfigDir, config.ConfigFileName)); statErr == nil {
		configFile, err := config.Load(dockerConfigDir)
		if err != nil {
			return nil, err
		}
		resolver, err = getResolver(ctx, configFile)
		if err != nil {
			return empty, err
		}
	} else if !os.IsNotExist(statErr) {
		return empty, statErr
	}

	var image containerd.Image
	if pullAlways {
		img, err := pullImage(ctx, client, resolver, imageName)
		if err != nil {
			return empty, err
		}

		image = img
	} else {
		img, err := client.GetImage(ctx, imageName)
		if err != nil {
			if !errdefs.IsNotFound(err) {
				return empty, err
			}
			img, err := pullImage(ctx, client, resolver, imageName)
			if err != nil {
				return empty, err
			}
			image = img
		} else {
			image = img
		}
	}

	unpacked, err := image.IsUnpacked(ctx, snapshotter)
	if err != nil {
		return empty, fmt.Errorf("cannot check if unpacked: %s", err)
	}

	if !unpacked {
		if err := image.Unpack(ctx, snapshotter); err != nil {
			return empty, fmt.Errorf("cannot unpack: %s", err)
		}
	}

	return image, nil
}

func pullImage(ctx context.Context, client *containerd.Client, resolver remotes.Resolver, imageName string) (containerd.Image, error) {

	var empty containerd.Image

	rOpts := []containerd.RemoteOpt{
		containerd.WithPullUnpack,
	}

	if resolver != nil {
		rOpts = append(rOpts, containerd.WithResolver(resolver))
	}

	img, err := client.Pull(ctx, imageName, rOpts...)
	if err != nil {
		return empty, fmt.Errorf("cannot pull: %s", err)
	}

	return img, nil
}
