package task

import (
	"encoding/json"
	"fmt"
	"os"
	"sync"

	"github.com/coreos/go-etcd/etcd"
)

const (
	EtcdToken = "{{etcd-url}}"
)

// Source provides access to a list of tasks
type Source interface {
	Tasks() ([]*ContainerUpdateTask, error)
	Name() string
}

type StaticSource struct {
	taskLock sync.RWMutex
	tasks    []*ContainerUpdateTask
}

type fileSource struct {
	taskDefFile string
}

type etcdSource struct {
	etcdURL string
	key     string
}

func NewStaticSource(tasks ...*ContainerUpdateTask) *StaticSource {
	source := &StaticSource{tasks: make([]*ContainerUpdateTask, 0)}
	for _, t := range tasks {
		source.tasks = append(source.tasks, t)
	}
	return source
}

func (s *StaticSource) Tasks() ([]*ContainerUpdateTask, error) {
	s.taskLock.RLock()
	defer s.taskLock.RUnlock()
	return s.tasks, nil
}

func (s *StaticSource) Name() string {
	return "Static Source"
}

func (s *StaticSource) AddTask(t *ContainerUpdateTask) {
	s.taskLock.Lock()
	defer s.taskLock.Unlock()
	s.tasks = append(s.tasks, t)
}

// NewFileSource creates a file-based task source
func NewFileSource(taskDefFile string) (source Source) {
	return &fileSource{taskDefFile: taskDefFile}
}

// NewEtcdSource creates a source that expects JSON encoded tasks
// to be stored as the values of child nodes of the given url.
func NewEtcdSource(etcdURL, key string) (source Source) {
	return &etcdSource{etcdURL: etcdURL, key: key}
}

func (s *fileSource) Name() string {
	return "FileSource: " + s.taskDefFile
}

func (s *fileSource) Tasks() (tasks []*ContainerUpdateTask, err error) {
	taskDefFile, err := os.Open(s.taskDefFile)
	if err != nil {
		return
	}
	defer taskDefFile.Close()
	decoder := json.NewDecoder(taskDefFile)
	err = decoder.Decode(&tasks)
	return
}

func (s *etcdSource) Name() string {
	return fmt.Sprintf("EtcdSource: %s%s", s.etcdURL, s.key)
}

func (s *etcdSource) Tasks() (tasks []*ContainerUpdateTask, err error) {
	foundTasks := make([]*ContainerUpdateTask, 0)
	client := etcd.NewClient([]string{s.etcdURL})
	response, err := client.Get(s.key, true, true)
	if err != nil {
		return nil, err
	}
	for _, taskNode := range response.Node.Nodes {
		var task ContainerUpdateTask
		if err := json.Unmarshal([]byte(taskNode.Value), &task); err != nil {
			return nil, err
		}
		task.ReplaceRestartURLToken(EtcdToken, s.etcdURL)
		foundTasks = append(foundTasks, &task)
	}
	return foundTasks, nil
}

// AllTasks takes multiple Sources and returns a list of all of their tasks.
// The returned list will be an empty or partial list if err != nil
func AllTasks(sources ...Source) (tasks []*ContainerUpdateTask, err error) {
	tasks = make([]*ContainerUpdateTask, 0)
	for _, s := range sources {
		t, err := s.Tasks()
		if err != nil {
			return tasks, err
		}
		tasks = append(tasks, t...)
	}
	return
}
