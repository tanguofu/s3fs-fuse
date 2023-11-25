package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/containerd/containerd"
	"github.com/containerd/containerd/namespaces"
	dockertypes "github.com/docker/docker/api/types"
	dockercontainer "github.com/docker/docker/api/types/container"
	dockerfilters "github.com/docker/docker/api/types/filters"
	dockerclient "github.com/docker/docker/client"
	"github.com/sirupsen/logrus"
	log "github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
)

// log key name for easy batch patch
const (
	ACTION            = "action"
	PODNAME           = "podname"
	PODNAMESPACE      = "podnamespace"
	CONTAINERID       = "containerid"
	CONTAINERNAME     = "containername"
	CONTAINERSKIP     = "containerskip"
	CONTAINERSTATUS   = "status"
	CONTAINEREXITCODE = "exitcode"
	ERROR             = "error"
	TIMEFORMAT        = "2006-01-02 15:04:05.000"
)

func Int64ToTime(created int64) string {

	seconds := created / 1000
	nanoseconds := (created % 1000) * 1000000
	timestamp := time.Unix(seconds, nanoseconds)
	return timestamp.Format(TIMEFORMAT)
}

type ContainerRuntime interface {
	List(namespace string, podname string, excludesidecar string) error
	Wait() (int64, error)
	Close()
}

type DockerRuntime struct {
	cli        *dockerclient.Client
	containers []dockertypes.Container
}

func NewDockerRuntime() (*DockerRuntime, error) {

	client, err := dockerclient.NewClientWithOpts(dockerclient.FromEnv, dockerclient.WithAPIVersionNegotiation())
	if err != nil || client == nil {
		return nil, fmt.Errorf("create docker client err: %v", err)
	}

	if _, err := client.Info(context.Background()); err != nil {
		return nil, fmt.Errorf("get docker info err: %v", err)
	}

	return &DockerRuntime{
		cli:        client,
		containers: make([]dockertypes.Container, 0),
	}, nil
}

func (r *DockerRuntime) List(namespace string, podname string, excludesidecar string) error {

	filterArgs := []dockerfilters.KeyValuePair{
		{Key: "label", Value: fmt.Sprintf("io.kubernetes.pod.namespace=%s", namespace)},
		{Key: "label", Value: fmt.Sprintf("io.kubernetes.pod.name=%s", podname)},
	}
	containers, err := r.cli.ContainerList(context.Background(), dockertypes.ContainerListOptions{
		All:     true,
		Filters: dockerfilters.NewArgs(filterArgs...),
	})

	if err != nil {
		return fmt.Errorf("list docker containers err: %v", err)
	}

	var cosfsCreated int64 = 0
	var foundContainers []dockertypes.Container
	var foundNames []string

	// https://docs.docker.com/engine/api/v1.43/#tag/Container/operation/ContainerList
	for _, container := range containers {

		logentry := log.WithFields(logrus.Fields{
			CONTAINERID:   container.ID,
			PODNAME:       podname,
			PODNAMESPACE:  namespace,
			CONTAINERSKIP: excludesidecar,
		})

		if len(container.Labels) == 0 {
			logentry.Info("not found labels")
			continue
		}

		ctype, ok := container.Labels["io.kubernetes.docker.type"]
		if !ok || ctype != "container" {
			// skip pause
			continue
		}

		containerName, ok := container.Labels["io.kubernetes.container.name"]
		if !ok {
			logentry.Warnf("not found the label io.kubernetes.container.name")
			continue
		}

		logentry = logentry.WithField(CONTAINERNAME, containerName)

		if strings.HasPrefix(containerName, excludesidecar) {
			logentry.Infof("skip sidecar")
			cosfsCreated = container.Created
			continue
		}

		foundContainers = append(foundContainers, container)
		foundNames = append(foundNames, containerName)
	}

	if cosfsCreated == 0 {
		return fmt.Errorf("found no cosfs container in pod %s/%s", namespace, podname)
	}

	for idx, container := range foundContainers {

		logentry := log.WithFields(logrus.Fields{
			CONTAINERID:   container.ID,
			PODNAME:       podname,
			PODNAMESPACE:  namespace,
			CONTAINERNAME: foundNames[idx],
		})

		// skip init container
		if container.Created <= cosfsCreated {
			containerCreate := Int64ToTime(container.Created)
			cosfsCreate := Int64ToTime(cosfsCreated)

			logentry.Warnf("skip init container which created: %s before the cosfs create:%s", containerCreate, cosfsCreate)
			continue
		}

		logentry.Infof("found one container in pod")
		r.containers = append(r.containers, container)
	}

	if len(r.containers) == 0 {
		return fmt.Errorf("found no container in pod %s/%s", namespace, podname)
	}

	return nil
}

func (r *DockerRuntime) Wait() (int64, error) {

	var exitcode int64 = 0
	for _, container := range r.containers {

		containerName := container.Labels["io.kubernetes.container.name"]
		logentry := log.WithFields(logrus.Fields{
			CONTAINERID:   container.ID,
			CONTAINERNAME: containerName,
		})

		inspect, err := r.cli.ContainerInspect(context.Background(), container.ID)
		if err != nil {
			logentry.WithField(ERROR, err).Warn("ContainerInspect failed")
			continue
		}
		if inspect.State == nil {
			logentry.WithField(ERROR, err).Warn("ContainerInspect inspect.State is nil")
			continue
		}

		logentry = log.WithField(CONTAINERSTATUS, inspect.State.Status)

		// check already exited
		if inspect.State.Status == "removing" || inspect.State.Status == "exited" || inspect.State.Status == "dead" {
			if inspect.State.ExitCode != 0 {
				exitcode = int64(inspect.State.ExitCode)
			}

			logentry.WithField(CONTAINEREXITCODE, inspect.State.ExitCode).Info("container is exited")
			continue
		}

		logentry.Infof("begin container wait")

		// wait next-exit
		statusCh, errCh := r.cli.ContainerWait(context.Background(), container.ID, dockercontainer.WaitConditionNextExit)
		select {
		case err := <-errCh:
			if err != nil {
				logentry.WithField(ERROR, err).Warn("container wait failed")
				continue
			}
		case status := <-statusCh:
			if status.StatusCode != 0 {
				exitcode = int64(status.StatusCode)
			}
			logentry.WithField(CONTAINEREXITCODE, status.StatusCode).Warn("container wait done")
		}
	}

	return exitcode, nil
}

func (r *DockerRuntime) Close() {
	if r.cli != nil {
		r.cli.Close()
	}
}

type ContainerdRuntime struct {
	cli        *containerd.Client
	containers []containerd.Container
}

func NewContainerdRuntime(containerdep string) (*ContainerdRuntime, error) {

	client, err := containerd.New(containerdep, containerd.WithDefaultNamespace("k8s.io"))
	if err != nil || client == nil {
		return nil, fmt.Errorf("no found containerd server, containerd: %s, err: %v", containerdep, err)
	}

	if _, err := client.Version(context.Background()); err != nil {
		return nil, fmt.Errorf("get containerd version err: %v", err)
	}

	return &ContainerdRuntime{
		cli:        client,
		containers: make([]containerd.Container, 0),
	}, nil
}

func (r *ContainerdRuntime) List(namespace string, podname string, excludesidecar string) error {

	//see https://github.com/containerd/containerd/blob/metadata/containers_test.go
	filters := fmt.Sprintf("labels.\"io.kubernetes.pod.namespace\"==%s,labels.\"io.kubernetes.pod.name\"==%s", namespace, podname)

	containers, err := r.cli.Containers(context.Background(), filters)
	if err != nil {
		return fmt.Errorf("list containerd err: %v", err)
	}

	var cosfsCreatedTime *time.Time = nil
	var foundContainers []containerd.Container
	var foundNames []string

	for _, container := range containers {

		logentry := log.WithFields(logrus.Fields{
			CONTAINERID:   container.ID(),
			PODNAME:       podname,
			PODNAMESPACE:  namespace,
			CONTAINERSKIP: excludesidecar,
		})

		labels, err := container.Labels(context.Background())
		if err != nil {
			logentry.WithField(ERROR, err).Warn("list containerd containers")
			continue
		}

		ctype, ok := labels["io.cri-containerd.kind"]
		if !ok || ctype != "container" {
			// skip pause
			continue
		}

		containerName, ok := labels["io.kubernetes.container.name"]
		if !ok {
			logentry.Warn("list containerd not found io.kubernetes.container.name")
			continue
		}
		logentry = logentry.WithField(CONTAINERNAME, containerName)

		info, err := container.Info(context.Background())
		if err != nil {
			logentry.WithField(ERROR, err).Warn("get containerd info failed")
			continue
		}

		if strings.HasPrefix(containerName, excludesidecar) {
			logentry.Infof("skip sidecar")
			cosfsCreatedTime = &info.CreatedAt
			continue
		}

		foundContainers = append(foundContainers, container)
		foundNames = append(foundNames, containerName)
	}

	if cosfsCreatedTime == nil {
		return fmt.Errorf("found no cosfs container in pod %s/%s", namespace, podname)
	}

	for idx, container := range foundContainers {

		logentry := log.WithFields(logrus.Fields{
			CONTAINERID:   container.ID,
			PODNAME:       podname,
			PODNAMESPACE:  namespace,
			CONTAINERNAME: foundNames[idx],
		})

		// skip init container
		info, _ := container.Info(context.Background())
		if info.CreatedAt.Before(*cosfsCreatedTime) {

			logentry.Warnf("skip init container which created:%s before the cosfs create:%s",
				info.CreatedAt.Format(TIMEFORMAT),
				cosfsCreatedTime.Format(TIMEFORMAT))
			continue
		}

		logentry.Infof("found one container in pod")
		r.containers = append(r.containers, container)
	}

	if len(r.containers) == 0 {
		return fmt.Errorf("found no container in pod %s/%s", namespace, podname)
	}

	return nil
}

func (r *ContainerdRuntime) Wait() (int64, error) {

	var exitcode int64 = 0
	ctx := namespaces.WithNamespace(context.Background(), "k8s.io")

	for _, container := range r.containers {

		labels, _ := container.Labels(ctx)
		containerName := labels["io.kubernetes.container.name"]

		logentry := log.WithFields(logrus.Fields{
			CONTAINERID:   container.ID(),
			CONTAINERNAME: containerName,
		})

		task, err := container.Task(ctx, nil)
		if err != nil {
			logentry.WithField(ERROR, err).Info("get task from container failed")
			continue
		}

		status, err := task.Status(ctx)
		if err != nil {
			logentry.WithField(ERROR, err).Info("get task status from container failed")
			continue
		}

		logentry = logentry.WithField(CONTAINERSTATUS, status.Status)
		if status.Status == containerd.Stopped {
			if status.ExitStatus != 0 {
				exitcode = int64(status.ExitStatus)
			}
			logentry.WithField(CONTAINEREXITCODE, status.ExitStatus).Info("container is exited")
			continue
		}

		logentry.Infof("begin container wait")
		statusC, err := task.Wait(ctx)
		if err != nil {
			logentry.Infof("container wait failed")
			continue
		}

		waitstatus := <-statusC
		code, _, err := waitstatus.Result()

		if err != nil {
			logentry.WithField(ERROR, err).Warn("container wait status failed")
			continue
		}

		if code != 0 {
			exitcode = int64(code)
		}

		logentry.WithField(CONTAINEREXITCODE, code).Warn("container wait done")
	}

	return exitcode, nil
}

func (r *ContainerdRuntime) Close() {
	if r.cli != nil {
		r.cli.Close()
	}
}

type Options struct {
	ContainerdOnly bool // for test containerd only
	Containerd     string
	ExcludeSidecar string
	Namespace      string
	PodName        string
}

var options Options

var rootCmd = &cobra.Command{
	Use:              "sidecar",
	Short:            "check the containers of pods ",
	Long:             `A longer description of my application.`,
	PersistentPreRun: validation,
}

var checkCmd = &cobra.Command{
	Use:   "check",
	Short: "Check command",
	Long:  `A longer description of the check command.`,
	Run:   check,
}

var waitCmd = &cobra.Command{
	Use:   "wait",
	Short: "Wait command",
	Long:  `A longer description of the wait command.`,
	Run:   wait,
}

func NewRuntime(containerd string) ContainerRuntime {
	var r ContainerRuntime
	var err error

	if !options.ContainerdOnly {
		r, err = NewDockerRuntime()
		if err == nil {
			log.Info("connect docker ok")
			return r
		}

		log.WithField("ContainerdOnly", options.ContainerdOnly).WithField(ERROR, err).Warn("try connect docker failed")
	}

	r, err = NewContainerdRuntime(containerd)
	if err == nil {
		log.WithField("containerd", containerd).Info("connect containerd ok")
		return r
	}

	log.WithField("containerd", containerd).WithField(ERROR, err).Warn("try connect containerd failed")
	return nil

}

func validation(cmd *cobra.Command, args []string) {
	options.ContainerdOnly = false

	if val := os.Getenv("CONTAINERD_ONLY"); len(val) > 0 {
		options.ContainerdOnly = true
	}

	if val := os.Getenv("POD_NAME"); len(val) > 0 {
		options.PodName = val
	}

	if val := os.Getenv("POD_NAMESPACE"); len(val) > 0 {
		options.Namespace = val
	}

	if len(options.PodName) == 0 || len(options.Namespace) == 0 {
		log.WithFields(logrus.Fields{
			PODNAME:      options.PodName,
			PODNAMESPACE: options.Namespace,
		}).Fatal("POD_NAME and POD_NAMESPACE env var must be set ")
	}
}

func check(cmd *cobra.Command, args []string) {

	r := NewRuntime(options.Containerd)
	if r == nil {
		log.Fatalf("can not create docker or containerd runtime")
		return
	}
	defer r.Close()

	if err := r.List(options.Namespace, options.PodName, options.ExcludeSidecar); err != nil {
		log.WithField(ERROR, err).Fatal("list containers failed")
		return
	}
}

func wait(cmd *cobra.Command, args []string) {
	r := NewRuntime(options.Containerd)
	if r == nil {
		log.Fatalf("can not create docker or containerd runtime")
		return
	}
	defer r.Close()

	if err := r.List(options.Namespace, options.PodName, options.ExcludeSidecar); err != nil {
		log.WithField(ERROR, err).Fatal("list containers failed")
		return
	}

	code, _ := r.Wait()
	os.Exit(int(code))
}

func init() {
	rootCmd.PersistentFlags().StringVar(&options.Containerd, "containerd", "/var/run/containerd/containerd.sock", "default is /var/run/containerd/containerd.sock")
	rootCmd.PersistentFlags().StringVar(&options.ExcludeSidecar, "sidecar", "ti-cosfs-", "the sidecar name prefix of container, default is ti-cosfs-")
	rootCmd.PersistentFlags().StringVar(&options.Namespace, "namespace", "", "the namespace of pod")
	rootCmd.PersistentFlags().StringVar(&options.PodName, "podname", "", "the podname of pod")
	rootCmd.AddCommand(checkCmd)
	rootCmd.AddCommand(waitCmd)
}

func main() {

	log.SetFormatter(&logrus.TextFormatter{
		QuoteEmptyFields: true,
	})
	log.SetReportCaller(true)

	if err := rootCmd.Execute(); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}
