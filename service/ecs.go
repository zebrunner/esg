package service

import (
	"errors"
	"fmt"
	"math/rand"

	"github.com/aws/aws-sdk-go/service/ecrpublic"

	"github.com/aws/aws-sdk-go/service/s3"

	"net/url"
	"strconv"
	"time"

	"github.com/aerokube/util"
	"github.com/zebrunner/esg/config"
	"github.com/zebrunner/esg/session"

	"strings"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/client"
	awsSession "github.com/aws/aws-sdk-go/aws/session"

	//	"github.com/aws/aws-sdk-go/aws/request"
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
	sess, err := awsSession.NewSession(&aws.Config{
		Region:     &config.AwsRegion,
		MaxRetries: &config.AwsRetry,
		Retryer: client.DefaultRetryer{
			MaxThrottleDelay: 30 * time.Second,
			MinThrottleDelay: 5 * time.Second,
		},
	})
	if err != nil {
		return nil, err
	}

	return sess, nil
}

const (
	SeleniumPort   = 4444
	VncPort        = 5900
	DevtoolsPort   = 7070
	FileServerPort = 8080
	ClipboardPort  = 9090
)

func ListBrowsers() ([]string, error) {
	sess, err := awsSession.NewSession(&aws.Config{
		Region:     aws.String("us-east-1"),
		MaxRetries: &config.AwsRetry,
		Retryer: client.DefaultRetryer{
			MaxThrottleDelay: 30 * time.Second,
			MinThrottleDelay: 5 * time.Second,
		},
	})
	if err != nil {
		return nil, err
	}

	svc := ecrpublic.New(sess)
	var images []string

	for _, repository := range config.SupportedBrowsers {
		input := ecrpublic.DescribeImagesInput{
			RegistryId:     aws.String("659932254483"),
			RepositoryName: &repository,
		}
		result, err := svc.DescribeImages(&input)
		if err != nil {
			return nil, err
		}
		for _, image := range result.ImageDetails {
			for _, tag := range image.ImageTags {
				images = append(images, repository+":"+*tag)
			}
		}
	}

	return images, nil
}

func CreateTaskDefinition(browser string, family string) (taskDefinition *ecs.TaskDefinition, err error) {
	svc := ecs.New(AwsSess)
	browserContainerName := "browser"
	sharedFolder := "/opt/zebrunner"
	sharedVolume := "data"

	taskDefinitionInput := &ecs.RegisterTaskDefinitionInput{
		NetworkMode: aws.String("bridge"),
		ContainerDefinitions: []*ecs.ContainerDefinition{
			{
				Name:              aws.String(browserContainerName),
				Image:             aws.String("public.ecr.aws/zebrunner/" + browser),
				Cpu:               aws.Int64(int64(config.MinCpu)),
				Essential:         aws.Bool(true), //If the essential parameter of a container is marked as true, the failure of that container will stop the task.
				Memory:            aws.Int64(int64(config.MinMemory)),
				MemoryReservation: aws.Int64(int64(config.MinMemoryReservation)),
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
					{
						Name:  aws.String("VERBOSE"),
						Value: aws.String("1"),
					},
				},
				PortMappings: []*ecs.PortMapping{
					{
						ContainerPort: aws.Int64(SeleniumPort),
						HostPort:      aws.Int64(0),
					},
					{
						ContainerPort: aws.Int64(FileServerPort),
						HostPort:      aws.Int64(0),
					},
					{
						ContainerPort: aws.Int64(ClipboardPort),
						HostPort:      aws.Int64(0),
					},
					{
						ContainerPort: aws.Int64(VncPort),
						HostPort:      aws.Int64(0),
					},
					{
						ContainerPort: aws.Int64(DevtoolsPort),
						HostPort:      aws.Int64(0),
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
				MountPoints: []*ecs.MountPoint{
					{
						ContainerPath: aws.String("/data"),
						ReadOnly:      aws.Bool(false),
						SourceVolume:  aws.String(sharedVolume),
					},
				},
			},
		},
		Family: aws.String(family),
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

func (d *Task) RunTask(family string, username string) (taskArn string, err error) {
	svc := ecs.New(AwsSess)

	memory, memErr := getEcsMemory(d.Caps)
	memoryReservation, memResErr := getEcsMemoryReservation(d.Caps)
	cpu, cpuErr := getEcsCpu(d.Caps)
	if memErr != nil || memResErr != nil || cpuErr != nil {
		return "", fmt.Errorf("error happend while parsing resources. Errors: [%v, %v, %v]", memErr, memResErr, cpuErr)
	}

	browserContainerName := "browser"
	id := uuid.New().String()

	// revision := int64(1)
	overrides := []*ecs.ContainerOverride{
		{
			Name: &browserContainerName,
			Environment: []*ecs.KeyValuePair{
				{
					Name:  aws.String("UUID"),
					Value: aws.String(id),
				},
				{
					Name:  aws.String("ENABLE_VNC"),
					Value: aws.String(strconv.FormatBool(d.Caps.VNC)),
				},
			},
			Cpu:               &cpu,
			Memory:            &memory,
			MemoryReservation: &memoryReservation,
		},
		{
			Name: aws.String("artifacts-uploader"),
			Environment: []*ecs.KeyValuePair{
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
		},
	}
	runTaskInput := &ecs.RunTaskInput{
		Cluster:        &config.AwsCluster,
		TaskDefinition: aws.String(family),
		Overrides:      &ecs.TaskOverride{ContainerOverrides: overrides},
	}

	sleep := rand.Intn(15)
	log.Printf("[SLEEP] [%d]", sleep)
	time.Sleep(time.Duration(sleep) * time.Second)

	resultRunTask, err := svc.RunTask(runTaskInput)
	// Not good solution by aws doesn't give a choice
	if err.Error() == "ClientException: TaskDefinition not found." {
		return "", fmt.Errorf("Browser %s not found", family)
	}
	//TODO: take a look to LastStatus field for negative cases. PROVISIONING and PENDING looks good. Extra varians?
	// log.Printf("[TASK_RUN_RESULT] [%v]", resultRunTask)
	isStarted := false
	for retryCount := 0; retryCount < config.RetryCount; retryCount++ {
		// TODO: explicitly minimize errors range to wait only by well-knoen reasons aka RESOURCE:CPU etc
		// TODO: convert existing hard-coded 25 retries into the queue or provisioning timeout: https://github.com/zebrunner/esg/issues/72
		for i := 1; i < 25; i++ { // [VD] "i" retry should be ~15 if instances can be started in 1 min and 25 if ~2 min
			if err != nil {
				log.WithError(err).WithField("attempt", i).Error("Task run error")
			} else if len(resultRunTask.Failures) > 0 {
				log.WithFields(log.Fields{
					"reason":  *resultRunTask.Failures[0].Reason,
					"attempt": i,
				}).Error("Task run failure")
			} else if len(resultRunTask.Tasks) == 0 {
				log.WithField("attempt", i).Error("Task run failure: result doesn't contains tasks")
			} else {
				// all good and we can proceed
				isStarted = true
				break
			}
			// sleep 1-15 sec for a while. TODO: reorganize into the smart delay
			sleep = rand.Intn(15)
			log.Printf("[SLEEP2] [%d]", sleep)
			time.Sleep(time.Duration(sleep) * time.Second)
			//time.Sleep(10 * time.Second)
			resultRunTask, err = svc.RunTask(runTaskInput)
		}

		if isStarted {
			time.Sleep(10 * time.Second)
			// task start initiated successfully. try to wait while running
			taskId := strings.Split(*resultRunTask.Tasks[0].TaskArn, "/")[2]

			// describeTaskInput := &ecs.DescribeTasksInput{
			// 	Cluster: &config.AwsCluster,
			// 	Tasks: []*string{
			// 		aws.String(taskId),
			// 	},
			// }

			//TODO: convert exiting hard-coded 5 wait attempts to dedicated waiter timeout
			// for i := 1; i < 25; i++ {
			// 	startTime := time.Now()
			// 	err = svc.WaitUntilTasksRunning(describeTaskInput)
			// 	//TODO: reuse wait with context to specify valid timeout
			// 	//err = svc.WaitUntilTasksRunningWithContext(aws.Context, describeTaskInput, request. WithWaiterDelay(60 * time.Second))
			// 	log.WithField("latency", time.Since(startTime)).Info("WaitUntilTasksRunning delay")
			// 	if err != nil {
			// 		log.WithError(err).WithField("attempt", retryCount).Error("Failed to wait for a task")
			// 		// repeit again run task and wait
			// 		continue
			// 	}
			// 	break
			// }

			startTime := time.Now()
			err = waitUntilTaskIsRunning(svc, taskId, ConstDelay(6*time.Second), 25)
			log.WithField("latency", time.Since(startTime)).Info("WaitUntilTasksRunning delay")
			if err != nil {
				log.WithError(err).WithField("taskId", taskId).Error("Failed to wait task successfull state")
			}
			// break
		}
		log.WithField("attempt", retryCount).Debug("retry failed")
	}

	/*
		Failures: [{
		      Arn: "arn:aws:ecs:us-east-1:659932254483:container-instance/829954d05541417cb21d02409e43ea10",
		      Reason: "RESOURCE:CPU"
		    }],
		  Tasks: []
		}]
	*/

	if err != nil {
		return "", err
	}

	return *resultRunTask.Tasks[0].TaskArn, nil
}

func ConstDelay(t time.Duration) func(int) time.Duration {
	return func(attempt int) time.Duration {
		return t
	}
}

func DeregisterTaskDefinition(family string) error {
	svc := ecs.New(AwsSess)
	input := ecs.DeregisterTaskDefinitionInput{
		TaskDefinition: aws.String(family + ":1"),
	}
	_, err := svc.DeregisterTaskDefinition(&input)
	if err != nil {
		return err
	}

	return nil
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

	_, err := StopTask(taskArn)
	if err != nil {
		log.WithError(err).WithField("taskARN", taskArn).Warn("Failed to stop task")
		return
	}
	log.WithField("taskARN", taskArn).Info("Task stopped")
}

func FindHostPort(container *ecs.Container, containerPort int) int64 {
	for _, b := range container.NetworkBindings {
		if *b.ContainerPort == int64(containerPort) {
			return *b.HostPort
		}
	}

	return 0
}

func (d *Task) GetStartedServiceInfo(taskArn string) (*StartedService, error) {
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

	var container *ecs.Container
	for _, c := range resultDescribeTask.Tasks[0].Containers {
		if *c.Name == "browser" {
			container = c
		}
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

	portConfig := ecsPortConfig{
		SeleniumPort:   FindHostPort(container, SeleniumPort),
		FileserverPort: FindHostPort(container, FileServerPort),
		ClipboardPort:  FindHostPort(container, ClipboardPort),
		VNCPort:        FindHostPort(container, VncPort),
		DevtoolsPort:   FindHostPort(container, FileServerPort),
	}

	hostPort := getTaskHostPort(d.Caps, privateIpAddress, &portConfig)
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

	s := StartedService{
		Url:      u,
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
	var err error

	parts := strings.Split(d.Service.Image, "/")
	browser := parts[len(parts)-1]
	browser = strings.ReplaceAll(browser, ":", "-")
	browser = strings.ReplaceAll(browser, ".", "-")

	startTime := time.Now()
	taskArn, err := d.RunTask(browser, username)
	if err != nil {
		return nil, fmt.Errorf("failed to start task. InternalError: %v", err)
	}
	log.WithField("latency", time.Since(startTime)).Info("RunTask delay")

	startTime = time.Now()
	sessionInfo, err := d.GetStartedServiceInfo(taskArn)
	if err != nil {
		RemoveTask(taskArn)
		return nil, fmt.Errorf("Failed to get service info. InternalError: %v", err)
	}

	err = wait(sessionInfo.Url.String(), d.StartupTimeout)
	if err != nil {
		RemoveTask(taskArn)
		return nil, fmt.Errorf("Session does not respond in %ds", d.StartupTimeout)
	}

	return sessionInfo, nil
}

func waitUntilTaskIsRunning(svc *ecs.ECS, taskId string, sleepFn func(int) time.Duration, maxAttempts int) error {
	for i := 0; i < maxAttempts; i++ {
		log := log.WithFields(log.Fields{
			"attempt": i,
			"taskId":  taskId,
		})

		describeTaskInput := &ecs.DescribeTasksInput{
			Cluster: &config.AwsCluster,
			Tasks:   []*string{&taskId},
		}
		describeTaskResult, err := svc.DescribeTasks(describeTaskInput)
		if err != nil {
			time.Sleep(sleepFn(i))
			continue
		}
		if len(describeTaskResult.Tasks) == 0 {
			log.Debug("Wait until task running. Got 0 tasks in result")
			time.Sleep(sleepFn(i))
			continue
		}
		if len(describeTaskResult.Failures) != 0 {
			log.WithField("failures", describeTaskResult.Failures).Debug("Wait until task running. For failures in response")
			time.Sleep(sleepFn(i))
			continue
		}

		if *describeTaskResult.Tasks[0].LastStatus == "RUNNING" {
			return nil
		}

		time.Sleep(sleepFn(i))
	}

	return errors.New("failed to wait successfull task status. Max etempt limit exceeded")
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
