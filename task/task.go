package task

import (
	"bufio"
	"crypto/sha256"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/samalba/dockerclient"
)

const (
	DefaultShutdownSignal = "KILL"
)

type Task interface {
	Run(referenceTask Task) (err error)
	ID() string
	Hash() string
}

type ContainerUpdateTask struct {
	// ShutdownSignal allows a signal other than the default (KILL) to be used when shutting down containers.
	ShutdownSignal string
	// CleanContainers causes all unused images to be removed after a container is removed
	CleanContainers bool
	// KeepVolumes allows volumes to be kept when the container is removed
	KeepVolumes bool
	// RestartTriggerURL, if provided, will be monitored for changes and the container will restart if a change is detected
	// even if the source has not been updated. This may be useful e.g. for restarting a container on config change.
	RestartTriggerURL string
	// Container is a dockerclient container object that will be used to create and start a container
	Container *dockerclient.ContainerConfig

	client             dockerclient.Client      `json:"-"`
	auth               *dockerclient.AuthConfig `json:"-"`
	restartTriggerHash string                   `json:"-"`
}

func (t *ContainerUpdateTask) ID() string {
	taskID := strings.Replace(t.Container.Image, "/", "-", -1)
	taskID = strings.Replace(taskID, ":", "_", -1)
	return taskID
}

func (t *ContainerUpdateTask) Hash() string {
	data, err := json.Marshal(t)
	if err != nil {
		return ""
	}
	hash := sha256.Sum256(data)
	return string(hash[:])
}

func (t *ContainerUpdateTask) Update(n *ContainerUpdateTask) {
	t.ShutdownSignal = n.ShutdownSignal
	t.CleanContainers = n.CleanContainers
	t.KeepVolumes = n.KeepVolumes
	t.RestartTriggerURL = n.RestartTriggerURL
	t.Container = n.Container
}

func (t *ContainerUpdateTask) ReplaceRestartURLToken(token, value string) {
	if t.RestartTriggerURL == "" {
		return
	}
	t.RestartTriggerURL = strings.Replace(t.RestartTriggerURL, token, value, -1)
}

func (t *ContainerUpdateTask) SetClient(client dockerclient.Client, auth *dockerclient.AuthConfig) {
	t.client = client
	t.auth = auth
}

func (t *ContainerUpdateTask) NeedsRestart() (needsRestart bool) {
	if t.RestartTriggerURL == "" {
		return
	}
	resp, err := http.Get(t.RestartTriggerURL)
	if err != nil {
		log.Printf("Failed to retrieve restart trigger (%s): %s", t.RestartTriggerURL, err)
		return
	}
	defer resp.Body.Close()

	sha := sha256.New()
	if _, err := io.Copy(sha, resp.Body); err != nil {
		log.Printf("Failed to read restart trigger (%s): %s", t.RestartTriggerURL, err)
		return
	}
	data := make([]byte, 0, sha256.Size)
	newHash := string(sha.Sum(data))
	if newHash != t.restartTriggerHash {
		needsRestart = true
	}
	t.restartTriggerHash = newHash
	return
}

func (t *ContainerUpdateTask) Run(referenceTask Task) (err error) {
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
	needsRestart := t.NeedsRestart()
	if referenceTask != nil {
		if t.Hash() != referenceTask.Hash() {
			needsRestart = true
			t.Update(referenceTask.(*ContainerUpdateTask))
		}
	}
	if initialImage != nil && newImage.Id == initialImage.Id && !needsRestart {
		log.Println("No change for", t.Container.Image, newImage.Id)
		return
	}
	err = t.Restart(newImage.Id, newImage.Created.Format(time.RFC3339), initialImage.Id)
	return
}

func (t *ContainerUpdateTask) Pull() (err error) {
	imageID := t.Container.Image
	ntag := strings.LastIndex(imageID, ":")
	nslash := strings.LastIndex(imageID, "/")
	tag := "latest"
	if ntag > 0 && ntag > nslash {
		tag = imageID[ntag+1:]
		imageID = imageID[:ntag]
	}
	imageID = imageID + ":" + tag
	log.Println("Pulling", imageID)
	return t.client.PullImage(imageID, t.auth)
}

func (t *ContainerUpdateTask) CurrentImage() (image *dockerclient.ImageInfo, err error) {
	return t.client.InspectImage(t.Container.Image)
}

func (t *ContainerUpdateTask) Restart(imageID, created, previousImageID string) (err error) {
	log.Println("Restarting", t.Container.Image)
	currentContainers, err := t.client.ListContainers(true, false, "")
	if err != nil {
		return
	}
	containers := make([]dockerclient.Container, 0)
	for _, c := range currentContainers {
		if c.Image == previousImageID || c.Image == t.Container.Image {
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
	log.Println("Creating container", t.Container.Image)
	containerID, err := t.client.CreateContainer(t.Container, "", t.auth)
	if err != nil {
		return
	}
	log.Println("Starting container", containerID)
	err = t.client.StartContainer(containerID, &t.Container.HostConfig)
	if err != nil {
		return
	}
	shutdownSignal := t.ShutdownSignal
	if shutdownSignal == "" {
		shutdownSignal = DefaultShutdownSignal
	}
	for _, c := range containers {
		log.Println("Stopping container", c.Id, "with", shutdownSignal)
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
