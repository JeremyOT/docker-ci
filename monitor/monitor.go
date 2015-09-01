package monitor

import (
	"encoding/base64"
	"encoding/json"
	"log"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/JeremyOT/docker-ci/task"
	"github.com/samalba/dockerclient"
)

const (
	DockerSocketURL = "unix:///var/run/docker.sock"
)

type Config struct {
	PollInterval   time.Duration `flag:"poll-interval,How often to attempt to pull updated containers,5m"`
	URL            string        `flag:"docker-url,The url for the docker daemon,unix:///var/run/docker.sock"`
	CommandAddress string        `flag:"command-address,The address to listen for commands on,127.0.0.1:5858"`
	AuthConfig     string        `flag:"auth-config,A base64 encoded JSON object containing credentials for pulling from the registry"`
}

type Monitor struct {
	taskLock       sync.RWMutex
	config         *Config
	client         dockerclient.Client
	authConfig     *dockerclient.AuthConfig
	commandAddress net.Addr
	quit           chan struct{}
	wait           chan struct{}
	trigger        chan struct{}
	started        chan struct{}
	tasks          map[string]task.Task
}

func New(config *Config) (m *Monitor, err error) {
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
	client, err := dockerclient.NewDockerClient(config.URL, nil)
	if err != nil {
		return
	}
	m = &Monitor{
		config:     config,
		client:     client,
		tasks:      make(map[string]task.Task, 10),
		authConfig: authConfig,
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

func (m *Monitor) writeResponse(writer http.ResponseWriter, data interface{}, status int) {
	if data != nil {
		writer.Header().Set("Content-Type", "application/json")
	}
	writer.WriteHeader(status)
	if data != nil {
		body, err := json.Marshal(data)
		if err != nil {
			log.Println("Error marshalling response:", err)
			return
		}
		_, err = writer.Write(body)
		if err != nil {
			log.Println("Error writing response:", err)
			return
		}
	}
}

func (m *Monitor) handleCommandRequest(writer http.ResponseWriter, request *http.Request) {
	path := request.URL.Path
	method := request.Method
	log.Println("Received command request", method, path)
	pathComponents := strings.Split(path, "/")
	switch pathComponents[1] {
	case "started":
		m.WaitForStart()
		m.writeResponse(writer, map[string]bool{"started": true}, 200)
	case "tasks":
		if len(pathComponents) == 2 {
			tasks := m.Tasks()
			task_ids := make([]string, 0, len(tasks))
			for _, task := range tasks {
				task_ids = append(task_ids, task.ID())
			}
			m.writeResponse(writer, map[string]interface{}{"tasks": task_ids}, 200)
		} else {
			task := m.tasks[pathComponents[2]]
			if task == nil {
				m.writeResponse(writer, map[string]interface{}{"error": "not found"}, 404)
			} else {
				m.writeResponse(writer, map[string]interface{}{"task": task}, 200)
			}
		}
	default:
		log.Println("Invalid command")
		m.writeResponse(writer, map[string]string{"error": "invalid command"}, 400)
	}
}

func (m *Monitor) run(listener net.Listener) {
	defer listener.Close()
	server := &http.Server{Handler: http.HandlerFunc(m.handleCommandRequest)}
	go server.Serve(listener)
	log.Println("Starting monitor. Polling every", m.config.PollInterval)
	if m.authConfig != nil {
		log.Println("Authenticated for user:", m.authConfig.Username)
	}
	log.Println("Listening for commands on", m.commandAddress)
	m.PerformTasks()
	close(m.started)
	ticker := time.Tick(m.config.PollInterval)
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

func (m *Monitor) CommandAddress() net.Addr {
	return m.CommandAddress()
}

func (m *Monitor) Start() (err error) {
	m.quit = make(chan struct{})
	m.wait = make(chan struct{})
	m.trigger = make(chan struct{})
	m.started = make(chan struct{})
	network := "tcp"
	commandAddress := m.config.CommandAddress
	if strings.HasPrefix(commandAddress, "unix://") {
		network = "unix"
		commandAddress = commandAddress[7:]
	}
	listener, err := net.Listen(network, commandAddress)
	if err != nil {
		return
	}
	m.commandAddress = listener.Addr()
	go m.run(listener)
	return
}

func (m *Monitor) Wait() {
	<-m.wait
}

func (m *Monitor) WaitForStart() {
	<-m.started
}

func (m *Monitor) Stop() {
	close(m.quit)
	m.Wait()
}
