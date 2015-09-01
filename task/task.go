package task

import (
	"bufio"
	"fmt"
	"io"
	"log"
	"strings"
	"time"

	"github.com/samalba/dockerclient"
)

const (
	DefaultShutdownSignal = "KILL"
)

type Task interface {
	Run() (err error)
	ID() string
}

type ContainerUpdateTask struct {
	ShutdownSignal  string
	CleanContainers bool
	KeepVolumes     bool
	Container       *dockerclient.ContainerConfig
	client          dockerclient.Client
	auth            *dockerclient.AuthConfig
}

func (t *ContainerUpdateTask) ID() string {
	return fmt.Sprintf("Container-%s", t.Container.Image)
}

func (t *ContainerUpdateTask) SetClient(client dockerclient.Client, auth *dockerclient.AuthConfig) {
	t.client = client
	t.auth = auth
}

func (t *ContainerUpdateTask) Run() (err error) {
	initialImage, err := t.CurrentImage()
	if err != nil && err.Error() != "Not found" {
		log.Println("Failed to read current image:", err)
		return
	}
	if err = t.Pull(); err != nil {
		log.Println("Failed to pull container:", err)
		return
	}
	time.Sleep(time.Second)
	newImage, err := t.CurrentImage()
	if err != nil {
		log.Println("Failed to read new image:", err)
		return
	}
	if initialImage != nil && newImage.Id == initialImage.Id {
		log.Println("No change for", t.Container.Image, newImage.Id)
		return
	}
	err = t.Restart(newImage.Id, newImage.Created.String())
	return
}

func (t *ContainerUpdateTask) Pull() (err error) {
	log.Println("Pulling", t.Container.Image)
	ntag := strings.LastIndex(t.Container.Image, ":")
	nslash := strings.LastIndex(t.Container.Image, "/")
	tag := "latest"
	if ntag > 0 && ntag > nslash {
		tag = t.Container.Image[ntag+1:]
	}
	return t.client.PullImage(t.Container.Image+":"+tag, t.auth)
}

func (t *ContainerUpdateTask) CurrentImage() (image *dockerclient.ImageInfo, err error) {
	return t.client.InspectImage(t.Container.Image)
}

func (t *ContainerUpdateTask) Restart(imageID, created string) (err error) {
	currentContainers, err := t.client.ListContainers(true, false, "")
	if err != nil {
		return
	}
	containers := make([]dockerclient.Container, 0)
	for _, c := range currentContainers {
		if c.Image == t.Container.Image {
			containers = append(containers, c)
		}
	}
	envLength := 0
	defer func(env []string) { t.Container.Env = env }(t.Container.Env)
	if t.Container.Env != nil {
		envLength += len(t.Container.Env)
	}
	env := make([]string, envLength, envLength+2)
	if t.Container.Env != nil {
		copy(env, t.Container.Env)
	}
	env = append(env, "DOCKER_IMAGE_CREATED="+created, "DOCKER_IMAGE_ID="+imageID)
	t.Container.Env = env
	containerID, err := t.client.CreateContainer(t.Container, "")
	if err != nil {
		return
	}
	err = t.client.StartContainer(containerID, &t.Container.HostConfig)
	if err != nil {
		return
	}
	shutdownSignal := t.ShutdownSignal
	if shutdownSignal == "" {
		shutdownSignal = DefaultShutdownSignal
	}
	for _, c := range containers {
		err = t.client.KillContainer(c.Id, shutdownSignal)
		if err != nil {
			return
		}
		log.Printf("Waiting for container %s to shutdown...", c.Id)
		logReader, err := t.client.ContainerLogs(c.Id, &dockerclient.LogOptions{
			Follow:     true,
			Stdout:     true,
			Stderr:     true,
			Timestamps: true,
			Tail:       2,
		})
		if err != nil {
			return err
		}
		lineReader := bufio.NewReader(logReader)
		for {
			if line, err := lineReader.ReadString('\n'); err != nil {
				if err != io.EOF {
					log.Println(err)
				}
				break
			} else {
				log.Printf("%s: %s", c.Id, line)
			}
		}
		log.Printf("Container %s is down", c.Id)
		log.Printf("Cleaning up %s", c.Id)
		err = t.client.RemoveContainer(c.Id, false, !t.KeepVolumes)
		if err != nil {
			return err
		}
		log.Printf("Removed %s", c.Id)
	}
	if t.CleanContainers {
		log.Println("Cleaning unused images")
		dangling, err := t.client.ListImagesFiltered(false, map[string][]string{"dangling": []string{"true"}})
		if err != nil {
			return err
		}
		for _, image := range dangling {
			log.Printf("Removing dangling image %s", image.Id)
			_, err = t.client.RemoveImage(image.Id, true)
			if err != nil {
				return err
			}
		}
	}
	log.Printf("Finished updating %s", t.Container.Image)
	return
}
