package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/JeremyOT/docker-ci/monitor"
	"github.com/JeremyOT/docker-ci/task"
	"github.com/JeremyOT/structflag"
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
		taskEtcdURL   string
		taskEtcdKey   string
		logFile       string
	}{
		monitorConfig: &monitor.Config{},
	}
	structflag.StructToFlags("", config.monitorConfig)
	flag.StringVar(&config.taskFile, "task-def", "", "The path to a JSON file containing an array of task definitions.")
	flag.StringVar(&config.taskEtcdURL, "etcd-url", "", "The address of an etcd cluster to pull task-defs from.")
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
		fmt.Println("Logging to", config.logFile)
	}

	m, err := monitor.New(config.monitorConfig)
	if err != nil {
		log.Panicln("Error creating monitor:", err)
	}

	sources := make([]task.Source, 0)

	if config.taskFile != "" {
		sources = append(sources, task.NewFileSource(config.taskFile))
	}

	if config.taskEtcdURL != "" {
		sources = append(sources, task.NewEtcdSource(config.taskEtcdURL, config.taskEtcdKey))
	}

	for _, source := range sources {
		m.AddTaskSource(source)
	}
	sigChan := make(chan os.Signal)
	signal.Notify(sigChan, syscall.SIGKILL, syscall.SIGTERM, syscall.SIGQUIT)
	go monitorSignal(m, sigChan)
	m.Start()
	m.Wait()
}
