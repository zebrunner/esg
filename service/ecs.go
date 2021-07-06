package service

import (
	"fmt"

	"github.com/aws/aws-sdk-go/service/s3"

	"math/rand"
	"net/url"
	"strconv"
	"time"

	"github.com/aerokube/util"
	"github.com/zebrunner/esg/config"
	"github.com/zebrunner/esg/session"

	"strings"

	"github.com/aws/aws-sdk-go/aws"
	awsSession "github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/aws/aws-sdk-go/service/ecs"

	"github.com/google/uuid"
	log "github.com/sirupsen/logrus"
)

var (
	AwsSess *awsSession.Session
)

// Task - ecs task container manager
type Task struct {
	ServiceBase
	Environment
	session.Caps
}

type ecsPortConfig struct {
	SeleniumPort   int64
	FileserverPort int64
	ClipboardPort  int64
	DevtoolsPort   int64
	VNCPort        int64
}

func InitAws() (*awsSession.Session, error) {
	sess, err := awsSession.NewSession(&aws.Config{Region: &config.AwsRegion, MaxRetries: &config.AwsRetry})
	if err != nil {
		return nil, err
	}

	return sess, nil
}

func getEcsPortConfig() *ecsPortConfig {
	selelinumPort := rand.Int63n(64511) + 1025
	fileserverPort := rand.Int63n(64511) + 1025
	clipboardPort := rand.Int63n(64511) + 1025
	vncPort := rand.Int63n(64511) + 1025
	devtoolsPort := rand.Int63n(64511) + 1025
	return &ecsPortConfig{
		SeleniumPort:   selelinumPort,
		FileserverPort: fileserverPort,
		ClipboardPort:  clipboardPort,
		VNCPort:        vncPort,
		DevtoolsPort:   devtoolsPort}
}

func (d *Task) CreateTaskDefinition(username string, portConfig *ecsPortConfig) (taskDefinition *ecs.TaskDefinition, err error) {
	svc := ecs.New(AwsSess)

	imageUrl := d.Service.Image
	browserContainerName := "browser"
	taskDefFamily := d.Caps.Name
	memory, memErr := getEcsMemory(d.Caps)
	memoryReservation, memResErr := getEcsMemoryReservation(d.Caps)
	cpu, cpuErr := getEcsCpu(d.Caps)
	if memErr != nil || memResErr != nil || cpuErr != nil {
		return nil, fmt.Errorf("error happend while parsing resources. Errors: [%v, %v, %v]", memErr, memResErr, cpuErr)
	}

	id := uuid.New().String()

	sharedFolder := "/opt/zebrunner"
	sharedVolume := "data"

	taskDefinitionInput := &ecs.RegisterTaskDefinitionInput{
		NetworkMode: aws.String("bridge"),
		ContainerDefinitions: []*ecs.ContainerDefinition{
			{
				Name:              aws.String(browserContainerName),
				Image:             aws.String(imageUrl),
				Cpu:               &cpu,
				Essential:         aws.Bool(true), //If the essential parameter of a container is marked as true, the failure of that container will stop the task.
				Memory:            &memory,
				MemoryReservation: &memoryReservation,
				Privileged:        aws.Bool(true), //privileged mode is needed to start browser driver correctly
				MountPoints: []*ecs.MountPoint{
					{
						ContainerPath: aws.String("/dev/shm"),
						ReadOnly:      aws.Bool(false),
						SourceVolume:  aws.String("devshm"),
					},
					{
						ContainerPath: aws.String(sharedFolder),
						ReadOnly:      aws.Bool(false),
						SourceVolume:  aws.String(sharedVolume),
					},
				},
				Environment: []*ecs.KeyValuePair{
					//TODO: provide extra values from caps
					{
						Name:  aws.String("UUID"),
						Value: aws.String(id),
					},
					{
						Name:  aws.String("VERBOSE"),
						Value: aws.String("1"),
					},
					{
						Name:  aws.String("ENABLE_VNC"),
						Value: aws.String(strconv.FormatBool(d.Caps.VNC)),
					},
				},
				PortMappings: []*ecs.PortMapping{
					{
						ContainerPort: aws.Int64(d.Service.Port),
						HostPort:      aws.Int64(portConfig.SeleniumPort),
					},
					{
						ContainerPort: aws.Int64(5900),
						HostPort:      aws.Int64(portConfig.VNCPort),
					},
					{
						ContainerPort: aws.Int64(7070),
						HostPort:      aws.Int64(portConfig.DevtoolsPort),
					},
					{
						ContainerPort: aws.Int64(8080),
						HostPort:      aws.Int64(portConfig.FileserverPort),
					},
					{
						ContainerPort: aws.Int64(9090),
						HostPort:      aws.Int64(portConfig.ClipboardPort),
					},
				},
			},
			{
				Name:              aws.String("artifacts-uploader"),
				Image:             aws.String("public.ecr.aws/zebrunner/artifacts-uploader:latest"),
				Essential:         aws.Bool(false), //If the essential parameter of a container is marked as true, the failure of that container will stop the task.
				Cpu:               aws.Int64(256),
				Memory:            aws.Int64(768),
				MemoryReservation: aws.Int64(768),
				Privileged:        aws.Bool(false), //no need privileged mode for artifacts-uploader/video-recording container
				Links: []*string{
					aws.String(browserContainerName),
				},
				Environment: []*ecs.KeyValuePair{
					//TODO: provide extra values from caps
					{
						Name:  aws.String("BROWSER_CONTAINER_NAME"),
						Value: aws.String(browserContainerName),
					},
					{
						Name:  aws.String("UUID"),
						Value: aws.String(id),
					},
					{
						Name:  aws.String("BUCKET"),
						Value: &config.S3Bucket,
					},
					{
						Name:  aws.String("TENANT"),
						Value: &username,
					},
					{
						Name:  aws.String("AWS_ACCESS_KEY_ID"),
						Value: &config.AwsAccessKeyID,
					},
					{
						Name:  aws.String("AWS_SECRET_ACCESS_KEY"),
						Value: &config.AwsSecretAccessKey,
					},
					{
						Name:  aws.String("AWS_DEFAULT_REGION"),
						Value: &config.AwsRegion,
					},
				},
				MountPoints: []*ecs.MountPoint{
					{
						ContainerPath: aws.String("/data"),
						ReadOnly:      aws.Bool(false),
						SourceVolume:  aws.String(sharedVolume),
					},
				},
				PortMappings: []*ecs.PortMapping{},
			},
		},
		Family: aws.String(taskDefFamily),
		Volumes: []*ecs.Volume{
			{
				Host: &ecs.HostVolumeProperties{
					SourcePath: aws.String("/dev/shm"),
				},
				Name: aws.String("devshm"),
			},
			{
				Host: &ecs.HostVolumeProperties{
					SourcePath: aws.String(sharedFolder),
				},
				Name: aws.String(sharedVolume),
			},
		},
		TaskRoleArn: aws.String(""),
	}

	resultTaskDefinition, err := svc.RegisterTaskDefinition(taskDefinitionInput)
	if err != nil {
		return nil, fmt.Errorf("unable to create task definition: %v", err)
	}

	return resultTaskDefinition.TaskDefinition, nil
}

func DeregisterTaskDefinition(taskDefinitionArn string) error {
	// svc := ecs.New(AwsSess)
	// taskDeregisterInput := &ecs.DeregisterTaskDefinitionInput{
	// 	TaskDefinition: aws.String(taskDefinitionArn),
	// }
	// resultTaskDeregister, err := svc.DeregisterTaskDefinition(taskDeregisterInput)
	// if err != nil {
	// 	log.WithError(err).WithField("taskDefinitionARN", taskDefinitionArn).Error("Failed to deregister task definition")
	// 	return err
	// }
	// log.WithField("taskDefinitionARN", *resultTaskDeregister.TaskDefinition.TaskDefinitionArn).Info("Task definition removed")
	// return nil
	return nil
}

func RunTask(taskDefinition *ecs.TaskDefinition) (taskArn string, err error) {
	svc := ecs.New(AwsSess)

	family := taskDefinition.Family
	revision := taskDefinition.Revision
	runTaskInput := &ecs.RunTaskInput{
		Cluster:        &config.AwsCluster,
		TaskDefinition: aws.String(*family + ":" + strconv.FormatInt(*revision, 10)),
	}
	resultRunTask, err := svc.RunTask(runTaskInput)
	if err != nil {
		return "", err
	}

	return *resultRunTask.Tasks[0].TaskArn, nil
}

func StopTask(taskArn string) (*ecs.StopTaskOutput, error) {
	svc := ecs.New(AwsSess)

	log.WithField("taskARN", taskArn).Info("Removing task")
	stopTaskInput := &ecs.StopTaskInput{
		Cluster: &config.AwsCluster,
		Reason:  aws.String("Cancel"),
		Task:    aws.String(taskArn),
	}

	result, err := svc.StopTask(stopTaskInput)
	if err != nil {
		log.WithError(err).WithField("taskARN", taskArn).Warn("Failed to stop task")
		return nil, err
	}

	return result, nil
}

// RemoveTask Method stops task by ARN and remove task-definition after that
func RemoveTask(taskArn string) {

	resultStopTask, err := StopTask(taskArn)
	if err != nil {
		log.WithError(err).WithField("taskARN", taskArn).Warn("Failed to stop task")
		return
	}
	log.WithField("taskARN", taskArn).Info("Task stopped")

	taskDefinitionArn := *resultStopTask.Task.TaskDefinitionArn
	err = DeregisterTaskDefinition(taskDefinitionArn)
	if err != nil {
		log.WithError(err).WithFields(log.Fields{
			"taskArn":           taskArn,
			"taskDefinitionArn": taskDefinitionArn,
		}).Error("Failed to deregister task definition")
		return
	}
	log.WithField("taskDefinitionARN", taskDefinitionArn).Info("Task definition removed")
}

func (d *Task) GetStartedServiceInfo(taskArn string, portConfig *ecsPortConfig) (*StartedService, error) {
	svc := ecs.New(AwsSess)

	taskId := strings.Split(taskArn, "/")[2]
	describeTaskInput := &ecs.DescribeTasksInput{
		Cluster: &config.AwsCluster,
		Tasks: []*string{
			aws.String(taskId),
		},
	}

	resultDescribeTask, err := svc.DescribeTasks(describeTaskInput)
	if err != nil {
		return nil, fmt.Errorf("unable to describe task: %v", err)
	}

	containerInstanceArn := *resultDescribeTask.Tasks[0].ContainerInstanceArn

	containerInstanceId := strings.Split(containerInstanceArn, "/")[2]
	log.WithFields(log.Fields{
		"taskContainerInstanceArn": containerInstanceArn,
		"taskContainerInstanceID":  containerInstanceId,
	}).Debug()

	containerInstanceInput := &ecs.DescribeContainerInstancesInput{
		Cluster: &config.AwsCluster,
		ContainerInstances: []*string{
			aws.String(containerInstanceId),
		},
	}
	resultContainerInstance, err := svc.DescribeContainerInstances(containerInstanceInput)
	if err != nil {
		return nil, fmt.Errorf("Unable to get container instance details: %v", err)
	}

	//TODO: verify that returned number of instances is 1!
	instanceId := *resultContainerInstance.ContainerInstances[0].Ec2InstanceId
	log.WithField("instanceID", instanceId).Debug()

	instanceInput := &ec2.DescribeInstancesInput{
		InstanceIds: []*string{
			aws.String(instanceId),
		},
	}

	svcEc2 := ec2.New(AwsSess)
	resultInstance, err := svcEc2.DescribeInstances(instanceInput)
	if err != nil {
		return nil, fmt.Errorf("Unable to get instance details: %v", err)
	}
	privateIpAddress := *resultInstance.Reservations[0].Instances[0].PrivateIpAddress
	publicIpAddress := *resultInstance.Reservations[0].Instances[0].PublicIpAddress
	log.WithFields(log.Fields{
		"instancePrivateIP": privateIpAddress,
		"instancePublicIP":  publicIpAddress,
	}).Debug()

	browserTaskStartTime := time.Now()
	log.WithFields(log.Fields{
		"taskID":        taskId,
		"taskStartTime": browserTaskStartTime,
	}).Debug()

	hostPort := getTaskHostPort(d.Caps, privateIpAddress, portConfig)
	log.WithField("hostPort", hostPort).Debug()
	log.WithField("VNCPort", hostPort.VNC).Debug("VNC")

	u := &url.URL{Scheme: "http", Host: hostPort.Selenium, Path: d.Service.Path}
	log.WithField("containerServiceUrl", u).Debug()

	serviceStartTime := time.Now()
	log.WithFields(log.Fields{
		"taskID":    taskId,
		"startTime": util.SecondsSince(serviceStartTime),
		"hostPort":  hostPort,
	}).Info("Service started")
	log.WithFields(log.Fields{
		"taskID":              taskId,
		"containerServiceUrl": u,
	}).Debug("Proxy to...")

	// publish all ports feature is still under question for ecs task service so empty map is ok
	var publishedPortsInfo map[string]string

	s := StartedService{
		Url: u,
		Container: &session.Container{
			ID:                  taskId,
			ContainerInstanceID: containerInstanceId,
			IPAddress:           privateIpAddress,
			Ports:               publishedPortsInfo,
		},
		TaskID:   taskId,
		HostPort: hostPort,
		Cancel: func() {
			RemoveTask(taskArn)
		},
	}

	return &s, nil
}

// StartWithCancel - Starter interface implementation
func (d *Task) StartWithCancel(username string) (*StartedService, error) {
	svc := ecs.New(AwsSess)

	portConfig := getEcsPortConfig()
	var err error
	for i := 0; i < config.RetryCount; i++ {
		log.WithField("attempt", i+1).Info("Session start attempt")
		startTime := time.Now()
		taskDefinition, err := d.CreateTaskDefinition(username, portConfig)
		log.WithField("latency", time.Since(startTime)).Info("CreateTaskDefinition delay")
		if err != nil {
			log.WithError(err).Error("Attempt failed")
			continue
		}

		startTime = time.Now()
		taskArn, err := RunTask(taskDefinition)
		log.WithField("latency", time.Since(startTime)).Info("RunTask delay")
		if err != nil {
			log.WithError(err).Error("Attempt failed")
			continue
		}
		taskId := strings.Split(taskArn, "/")[2]

		describeTaskInput := &ecs.DescribeTasksInput{
			Cluster: &config.AwsCluster,
			Tasks: []*string{
				aws.String(taskId),
			},
		}
		// time.Sleep(15 * time.Second)
		ScaleUp()

		startTime = time.Now()
		err = svc.WaitUntilTasksRunning(describeTaskInput)
		log.WithField("latency", time.Since(startTime)).Info("WaitUntilTasksRunning delay")

		if err != nil {
			RemoveTask(taskArn)
			failReason, reasonErr := getFailReason(svc, taskId)
			if reasonErr == nil {
				log.WithError(err).WithField("reason", failReason).Error("Attempt failed. Unable to wait until task is running")
			} else {
				log.WithError(err).Error("Attempt failed. Unable to wait until task is running")
			}
			continue
		}

		sessionInfo, err := d.GetStartedServiceInfo(taskArn, portConfig)
		if err != nil {
			log.WithError(err).Error("Attempt failed. Failed to get service info.")
			RemoveTask(taskArn)
			continue
		}

		err = wait(sessionInfo.Url.String(), d.StartupTimeout)
		if err != nil {
			log.WithError(err).Errorf("Session does not respond in %ds", d.StartupTimeout)
			RemoveTask(taskArn)
			continue
		}

		return sessionInfo, nil
	}

	return nil, fmt.Errorf("failed to start task after %d attempts. InternalError: %v", config.RetryCount, err)
}

func getFailReason(svc *ecs.ECS, taskId string) (*string, error) {
	describeTaskInput := &ecs.DescribeTasksInput{
		Cluster: &config.AwsCluster,
		Tasks:   []*string{aws.String(taskId)},
	}
	describeTaskResult, err := svc.DescribeTasks(describeTaskInput)
	if err != nil {
		return nil, err
	}

	resultReason := ""
	for _, container := range describeTaskResult.Tasks[0].Containers {
		if container.Reason != nil {
			resultReason += *container.Reason
		}
	}

	return &resultReason, nil
}

func parseResourceCapability(cap string, defaultValue int, capabilityName string) (int, error) {
	if cap == "" {
		return defaultValue, nil
	}
	resource, err := strconv.Atoi(cap)
	if err != nil {
		return 0, fmt.Errorf("unexpected %s capability format. %v Expected integer, got: %v", capabilityName, err, cap)
	}
	return resource, nil
}

func getEcsMemory(caps session.Caps) (int64, error) {
	memory, err := parseResourceCapability(caps.Memory, config.MinMemory, "Memory")
	if err != nil {
		return 0, err
	}
	if memory < config.MinMemory {
		fmt.Println("[WARN] Requested Memory is lower than MinMemory. Using MinMemory as default. Requested:", memory, "MinMemory:", config.MinMemory)
		return int64(config.MinMemory), nil
	} else if memory > config.MaxMemory {
		return 0, fmt.Errorf("Requested Memory is grater than MaxMemory allowed by system administrator. Requested: %d. MaxMemory: %d", memory, config.MaxMemory)
	}
	return int64(memory), nil
}

func getEcsMemoryReservation(caps session.Caps) (int64, error) {
	memoryReservation, err := parseResourceCapability(caps.MemoryReservation, config.MinMemoryReservation, "MemoryReservation")
	if err != nil {
		return 0, err
	}
	if memoryReservation < config.MinMemoryReservation {
		fmt.Println("[WARN] Requested MemoryReservation lower than MinMemoryReservation. Using MinMemoryReservation as default. Requested:", memoryReservation, "MinMemoryReservation:", config.MinMemoryReservation)
		return int64(config.MinMemoryReservation), nil
	} else if memoryReservation > config.MaxMemoryReservation {
		return 0, fmt.Errorf("Requested MemoryReservation is grater than MaxMemoryReservation allowed by system administrator. Requested: %d. MaxMemory: %d", memoryReservation, config.MaxMemoryReservation)
	}
	return int64(memoryReservation), nil
}

func getEcsCpu(caps session.Caps) (int64, error) {
	cpu, err := parseResourceCapability(caps.Cpu, config.MinCpu, "Cpu")
	if err != nil {
		return 0, err
	}
	if cpu < config.MinCpu {
		fmt.Println("[WARN] Requested CPU lower than MinCpu. Using MinCpu as default. Requested:", cpu, "MinCpu:", config.MinCpu)
		return int64(config.MinCpu), nil
	} else if cpu > config.MaxCpu {
		return 0, fmt.Errorf("Requested CPU is grater than MaxCpu allowed by system administrator. Requested: %d. MaxCpu: %d", cpu, config.MaxCpu)
	}
	return int64(cpu), nil
}

func getTaskHostPort(caps session.Caps, taskIP string, pc *ecsPortConfig) session.HostPort {
	containerIP := taskIP
	fn := func(containerPort int64) string {
		return containerIP + ":" + strconv.FormatInt(containerPort, 10)
	}

	hp := session.HostPort{
		Selenium:   fn(pc.SeleniumPort),
		Fileserver: fn(pc.FileserverPort),
		Clipboard:  fn(pc.ClipboardPort),
		Devtools:   fn(pc.DevtoolsPort),
	}

	if caps.VNC {
		hp.VNC = fn(pc.VNCPort)
	}

	return hp
}

func GeneratePreSignedURL(key string) (string, error) {
	s3Svc := s3.New(AwsSess)
	req, _ := s3Svc.GetObjectRequest(&s3.GetObjectInput{
		Bucket: &config.S3Bucket,
		Key:    &key,
	})
	urlStr, err := req.Presign(10 * time.Minute)
	if err != nil {
		return "", err
	}

	return urlStr, nil
}
