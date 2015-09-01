package main

import (
	"encoding/json"
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/JeremyOT/docker-ci/monitor"
	"github.com/JeremyOT/docker-ci/task"
	"github.com/JeremyOT/structflag"
	"github.com/coreos/go-etcd/etcd"
)

func monitorSignal(m *monitor.Monitor, sigChan <-chan os.Signal) {
	for sig := range sigChan {
		if sig == syscall.SIGQUIT {
			log.Println("Received signal", sig, "exiting gracefully")
			m.Stop()
		} else {
			log.Println("Received signal", sig, "exiting immediately")
			os.Exit(1)
		}
	}
}

func main() {
	config := struct {
		monitorConfig *monitor.Config
		taskFile      string
		taskEtcdUrl   string
		taskEtcdKey   string
		logFile       string
	}{
		monitorConfig: &monitor.Config{},
	}
	structflag.StructToFlags("", config.monitorConfig)
	flag.StringVar(&config.taskFile, "task-def", "", "The path to a JSON file containing an array of task definitions.")
	flag.StringVar(&config.taskEtcdUrl, "etcd-url", "", "The address of an etcd cluster to pull task-defs from.")
	flag.StringVar(&config.taskEtcdKey, "etcd-key", "", "The path to the directory in etcd where task-defs are stored. It is expected that each task def is stored as the value in its own node in the etcd cluster.")
	flag.StringVar(&config.logFile, "log-file", "", "The path to use for logging.")
	flag.Parse()

	if config.logFile != "" {
		logFile, err := os.OpenFile(config.logFile, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0666)
		if err != nil {
			log.Panicln("Failed to open log file", err)
		}
		defer logFile.Close()
		log.SetOutput(logFile)
		log.SetFlags(log.Ldate | log.Ltime | log.Lshortfile)
	}

	m, err := monitor.NewWithConfig(config.monitorConfig)
	if err != nil {
		log.Panicln("Error creating monitor:", err)
	}

	var tasks []*task.ContainerUpdateTask
	if config.taskFile != "" {
		taskDefFile, err := os.Open(config.taskFile)
		if err != nil {
			log.Panicln("Error reading task defs:", err)
		}
		decoder := json.NewDecoder(taskDefFile)
		if err = decoder.Decode(&tasks); err != nil {
			log.Panicln("Error reading task defs:", err)
		}
		taskDefFile.Close()
	} else {
		tasks = make([]*task.ContainerUpdateTask, 0)
	}

	if config.taskEtcdUrl != "" {
		client := etcd.NewClient([]string{config.taskEtcdUrl})
		response, err := client.Get(config.taskEtcdKey, false, true)
		if err != nil {
			log.Panicln("Error reading task defs:", err)
		}
		for _, taskNode := range response.Node.Nodes {
			var task task.ContainerUpdateTask
			if err := json.Unmarshal([]byte(taskNode.Value), &task); err != nil {
				log.Panicln("Error reading task defs:", err, "\n", taskNode.Value)
			}
			tasks = append(tasks, &task)
		}
	}

	for _, task := range tasks {
		m.AddTask(task)
	}
	sigChan := make(chan os.Signal)
	signal.Notify(sigChan, syscall.SIGKILL, syscall.SIGTERM, syscall.SIGQUIT)
	go monitorSignal(m, sigChan)
	m.Start()
	m.Wait()
}
