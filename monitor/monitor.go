package monitor

import (
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"log"
	"sync"
	"time"

	"github.com/JeremyOT/docker-ci/task"
	"github.com/samalba/dockerclient"
)

const (
	DockerSocketURL = "unix:///var/run/docker.sock"
)

type Config struct {
	PollInterval  time.Duration `flag:"poll-interval,How often to attempt to pull updated containers,5m"`
	URL           string        `flag:"docker-url,The url for the docker daemon,unix:///var/run/docker.sock"`
	ListenAddress string        `flag:"listen-address,The address to listen for commands on"`
	AuthConfig    string        `flag:"auth-config,A base64 encoded JSON object containing credentials for pulling from the registry"`
}

type Monitor struct {
	taskLock     sync.RWMutex
	pollInterval time.Duration
	client       dockerclient.Client
	authConfig   *dockerclient.AuthConfig
	quit         chan struct{}
	wait         chan struct{}
	trigger      chan struct{}
	tasks        map[string]task.Task
}

func NewWithConfig(config *Config) (*Monitor, error) {
	var authConfig *dockerclient.AuthConfig
	if config.AuthConfig != "" {
		authJson, err := base64.StdEncoding.DecodeString(config.AuthConfig)
		if err != nil {
			return nil, err
		}
		var auth dockerclient.AuthConfig
		if err = json.Unmarshal(authJson, &auth); err != nil {
			return nil, err
		}
		authConfig = &auth
	}
	return New(config.PollInterval, config.URL, authConfig, nil)
}

func New(pollInterval time.Duration, url string, authConfig *dockerclient.AuthConfig, tlsConfig *tls.Config) (m *Monitor, err error) {
	client, err := dockerclient.NewDockerClient(url, tlsConfig)
	if err != nil {
		return
	}
	m = &Monitor{
		pollInterval: pollInterval,
		client:       client,
		tasks:        make(map[string]task.Task, 10),
		authConfig:   authConfig,
	}
	return
}

func (m *Monitor) Tasks() (tasks []task.Task) {
	m.taskLock.RLock()
	defer m.taskLock.RUnlock()
	tasks = make([]task.Task, 0, len(m.tasks))
	for _, t := range m.tasks {
		tasks = append(tasks, t)
	}
	return
}

func (m *Monitor) AddTask(t task.Task) {
	m.taskLock.Lock()
	defer m.taskLock.Unlock()
	if containerUpdateTask, ok := t.(*task.ContainerUpdateTask); ok {
		containerUpdateTask.SetClient(m.client, m.authConfig)
	}
	m.tasks[t.ID()] = t
	log.Println("Added task:", t.ID())
}

func (m *Monitor) PerformTasks() {
	tasks := m.Tasks()
	for _, t := range tasks {
		if err := t.Run(); err != nil {
			log.Printf("Error running task %s: %s", t.ID(), err)
		}
	}
}

func (m *Monitor) run() {
	log.Println("Starting monitor. Polling every", m.pollInterval)
	if m.authConfig != nil {
		log.Println("Authenticated for user:", m.authConfig.Username)
	}
	m.PerformTasks()
	ticker := time.Tick(m.pollInterval)
	for {
		select {
		case <-m.quit:
			return
		case <-m.trigger:
			m.PerformTasks()
		case <-ticker:
			m.PerformTasks()
		}
	}
}

func (m *Monitor) Start() {
	m.quit = make(chan struct{})
	m.wait = make(chan struct{})
	go m.run()
}

func (m *Monitor) Wait() {
	<-m.wait
}

func (m *Monitor) Stop() {
	close(m.quit)
	m.Wait()
}
