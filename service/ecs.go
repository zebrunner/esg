package service

import (
	"fmt"

	"github.com/aws/aws-sdk-go/service/s3"

	"math/rand"
	"net/url"
	"strconv"
	"time"

	"github.com/aerokube/util"
	"github.com/zebrunner/esg/session"

	"strings"

	"github.com/aws/aws-sdk-go/aws"
	awsSession "github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/aws/aws-sdk-go/service/ecs"

	"github.com/google/uuid"
	log "github.com/sirupsen/logrus"
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

// StartWithCancel - Starter interface implementation
func (d *Task) StartWithCancel(username string) (*StartedService, error) {
	portConfig, err := getEcsPortConfig()
	if err != nil {
		return nil, fmt.Errorf("configuring ports: %v", err)
	}

	memory, memErr := getEcsMemory(d.Caps)
	memoryReservation, memResErr := getEcsMemoryReservation(d.Caps)
	cpu, cpuErr := getEcsCpu(d.Caps)
	if memErr != nil || memResErr != nil || cpuErr != nil {
		return nil, fmt.Errorf("error happend while parsing resources. Errors: [%v, %v, %v]", memErr, memResErr, cpuErr)
	}

	imageUrl := d.Service.Image

	// Without unique nano postfix we face with AWS limitations during multi-threading execution a lot...
	taskDefFamily := d.Caps.Name + "-" + strconv.Itoa(int(time.Now().UnixNano()))
	log.WithField("taskDefinitionFamily", taskDefFamily).Debug()

	//create ECS task definition based on capabilities
	sess, err := awsSession.NewSession(&aws.Config{Region: &AwsRegion, MaxRetries: &AwsRetry})
	if err != nil {
		return nil, err
	}
	svc := ecs.New(sess)

	//TODO: support GPU reservation: The number of GPU units to reserve for the container. A container instance with GPU support has 1 GPU unit for every GPU.
	//log.Printf("[CREATING_ECS_TASK_DEFINITION] [%s]", imageUrl)
	log.WithField("imageUrl", imageUrl).Info("Creating ECS task definition")

	uuid := uuid.New().String()

	sharedFolder := "/opt/zebrunner"
	sharedVolume := "data"

	browserContainerName := "browser"
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
						Value: aws.String(uuid),
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
						Value: aws.String(uuid),
					},
					{
						Name:  aws.String("BUCKET"),
						Value: &S3Bucket,
					},
					{
						Name:  aws.String("TENANT"),
						Value: &username,
					},
					{
						Name:  aws.String("AWS_ACCESS_KEY_ID"),
						Value: &AwsAccessKeyID,
					},
					{
						Name:  aws.String("AWS_SECRET_ACCESS_KEY"),
						Value: &AwsSecretAccessKey,
					},
					{
						Name:  aws.String("AWS_DEFAULT_REGION"),
						Value: &AwsRegion,
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

	time.Sleep(1 * time.Second)
	resultTaskDefinition, err := svc.RegisterTaskDefinition(taskDefinitionInput)
	if err != nil {
		return nil, fmt.Errorf("Unable to create task definition: %v", err)
	}

	taskStartTime := time.Now()
	//log.Printf("[STARTING_TASK] [%s] [%s]", imageUrl, taskStartTime)
	log.WithFields(log.Fields{
		"taskStartTime": taskStartTime,
		"imageUrl":      imageUrl,
	}).Debug()

	family := *resultTaskDefinition.TaskDefinition.Family
	revision := *resultTaskDefinition.TaskDefinition.Revision

	// Pass a context with a timeout to tell a blocking function that it should abandon its work after the timeout elapses.
	//TODO: parametrize provision timeout
	runTaskInput := &ecs.RunTaskInput{
		Cluster:        &AwsCluster,
		TaskDefinition: aws.String(family + ":" + strconv.FormatInt(revision, 10)),
	}

	resultRunTask, err := svc.RunTask(runTaskInput)
	taskFailure := ""
	for retry := 1; retry < 5; retry++ {
		if err != nil {
			//log.Printf("[TASK_RUN_ERROR] [%s] [%d]", err, retry)
			log.WithError(err).WithField("retry", retry).Warn("Task run attempt error")
		} else if len(resultRunTask.Failures) > 0 {
			taskFailure = *resultRunTask.Failures[0].Reason
			//log.Printf("[TASK_RUN_FAILURE] [%s] [%d]", taskFailure, retry)
			log.WithError(err).WithField("retry", retry).Error("Task run failure")
		} else {
			// all good and we can proceed
			taskFailure = "" //reset taskFailure if any
			break
		}

		// retry run task operation
		time.Sleep(5 * time.Second)
		resultRunTask, err = svc.RunTask(runTaskInput)
	}

	//TODO: add task definition removal for negative use-case
	if err != nil {
		return nil, fmt.Errorf("Unable to run task: %v", err)
	}
	if taskFailure != "" {
		return nil, fmt.Errorf("Unable to run task: %s", taskFailure)
	}

	taskArn := *resultRunTask.Tasks[0].TaskArn
	//log.Printf("[TASK_ARN] [%s]", taskArn)
	log.WithField("taskARN", taskArn).Debug()
	taskId := strings.Split(taskArn, "/")[2]
	//TODO: wait until container starts (in response we should have valid *resultRunTask.Tasks[0].ContainerInstanceArn value
	describeTaskInput := &ecs.DescribeTasksInput{
		Cluster: &AwsCluster,
		Tasks: []*string{
			aws.String(taskId),
		},
	}
	time.Sleep(15 * time.Second)
	// Check if task is in provisioning in running or provisioning task
	ScaleUp()

	err = svc.WaitUntilTasksRunning(describeTaskInput)
	if err != nil {
		RemoveTask(taskArn)
		failReason, reasonErr := getFailReason(svc, taskId)
		if reasonErr == nil {
			return nil, fmt.Errorf("Unable to wait until task is running: %v", *failReason)
		} else {
			return nil, fmt.Errorf("Unable to wait until task is running: %v", err)
		}
	}

	resultDescribeTask, err := svc.DescribeTasks(describeTaskInput)
	if err != nil {
		RemoveTask(taskArn)
		return nil, fmt.Errorf("Unable to describe task: %v", err)
	}

	containerInstanceArn := *resultDescribeTask.Tasks[0].ContainerInstanceArn

	//log.Printf("[TASK_CONTAINER_INSTANCE] [%s]", containerInstanceArn)
	containerInstanceId := strings.Split(containerInstanceArn, "/")[2]
	//log.Printf("[TASK_CONTAINER_INSTANCE_ID] [%s]", containerInstanceId)
	log.WithFields(log.Fields{
		"taskContainerInstanceArn": containerInstanceArn,
		"taskContainerInstanceID":  containerInstanceId,
	}).Debug()

	containerInstanceInput := &ecs.DescribeContainerInstancesInput{
		Cluster: &AwsCluster,
		ContainerInstances: []*string{
			aws.String(containerInstanceId),
		},
	}
	resultContainerInstance, err := svc.DescribeContainerInstances(containerInstanceInput)
	if err != nil {
		RemoveTask(taskArn)
		return nil, fmt.Errorf("Unable to get container instance details: %v", err)
	}

	//TODO: verify that returned number of instances is 1!
	instanceId := *resultContainerInstance.ContainerInstances[0].Ec2InstanceId
	//log.Printf("[INSTANCE_ID] [%s]", instanceId)
	log.WithField("instanceID", instanceId).Debug()

	instanceInput := &ec2.DescribeInstancesInput{
		InstanceIds: []*string{
			aws.String(instanceId),
		},
	}

	svcEc2 := ec2.New(sess)
	resultInstance, err := svcEc2.DescribeInstances(instanceInput)
	if err != nil {
		RemoveTask(taskArn)
		return nil, fmt.Errorf("Unable to get instance details: %v", err)
	}
	privateIpAddress := *resultInstance.Reservations[0].Instances[0].PrivateIpAddress
	//log.Printf("[INSTANCE_PRIVATE_IP] [%s]", privateIpAddress)
	publicIpAddress := *resultInstance.Reservations[0].Instances[0].PublicIpAddress
	//log.Printf("[INSTANCE_PUBLIC_IP] [%s]", publicIpAddress)
	log.WithFields(log.Fields{
		"instancePrivateIP": privateIpAddress,
		"instancePublicIP":  publicIpAddress,
	}).Debug()

	browserTaskStartTime := time.Now()
	//log.Printf("[TASK_STARTED] [%s] [%s] [%.2fs]", imageUrl, taskId, util.SecondsSince(browserTaskStartTime))
	log.WithFields(log.Fields{
		"imageURL":      imageUrl,
		"taskID":        taskId,
		"taskStartTime": browserTaskStartTime,
	}).Debug()

	hostPort := getTaskHostPort(d.Caps, publicIpAddress, portConfig)
	//log.Printf("[HOST_PORT] [%s]", hostPort)
	log.WithField("hostPort", hostPort).Debug()
	log.WithField("VNCPort", hostPort.VNC).Debug("VNC")

	u := &url.URL{Scheme: "http", Host: hostPort.Selenium, Path: d.Service.Path}
	//log.Printf("[CONTAINER_SERVICE_URL] [%s]", u)
	log.WithField("containerServiceUrl", u).Debug()

	serviceStartTime := time.Now()
	err = wait(u.String(), d.StartupTimeout)
	if err != nil {
		RemoveTask(taskArn)
		return nil, fmt.Errorf("wait: %v", err)
	}
	//log.Printf("[SERVICE_STARTED] [%s] [%s] [%.2fs]", imageUrl, taskId, util.SecondsSince(serviceStartTime))
	//log.Printf("[PROXY_TO] [%s] [%s]", taskId, u.String())
	log.WithFields(log.Fields{
		"imageURL":  imageUrl,
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

func getFailReason(svc *ecs.ECS, taskId string) (*string, error) {
	describeTaskInput := &ecs.DescribeTasksInput{
		Cluster: &AwsCluster,
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

func GetTasksCount() (*map[string]interface{}, error) {
	sess, err := awsSession.NewSession(&aws.Config{Region: &AwsRegion, MaxRetries: &AwsRetry})
	if err != nil {
		return nil, err
	}

	svc := ecs.New(sess)
	listInput := ecs.ListTasksInput{
		Cluster: &AwsCluster,
	}

	var tasks []*ecs.Task
	for {
		listResult, err := svc.ListTasks(&listInput)
		if err != nil {
			log.WithError(err).Error("Failed to get list of tasks")
			return nil, err
		}
		if len(listResult.TaskArns) == 0 {
			break
		}
		listInput.NextToken = listResult.NextToken

		describeInput := ecs.DescribeTasksInput{
			Cluster: &AwsCluster,
			Tasks:   listResult.TaskArns,
		}
		describeResult, err := svc.DescribeTasks(&describeInput)
		if err != nil {
			log.WithError(err).Error("Failed to describe tasks")
			return nil, err
		}
		tasks = append(tasks, describeResult.Tasks...)

		if listInput.NextToken == nil {
			break
		}
	}

	result := map[string]int{
		"PROVISIONING":   0,
		"PENDING":        0,
		"ACTIVATING":     0,
		"RUNNING":        0,
		"DEACTIVATING":   0,
		"STOPPING":       0,
		"DEPROVISIONING": 0,
		"STOPPED":        0,
	}
	for _, task := range tasks {
		result[*task.LastStatus] += 1
	}

	return &map[string]interface{}{
		"tasks": result,
	}, nil
}

func getEcsPortConfig() (*ecsPortConfig, error) {
	//TODO: implement unique ports generation maybe as external service/lambda to support stateless ecs-docker service
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
		DevtoolsPort:   devtoolsPort}, nil
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
	memory, err := parseResourceCapability(caps.Memory, MinMemory, "Memory")
	if err != nil {
		return 0, err
	}
	if memory < MinMemory {
		fmt.Println("[WARN] Requested Memory is lower than MinMemory. Using MinMemory as default. Requested:", memory, "MinMemory:", MinMemory)
		return int64(MinMemory), nil
	} else if memory > MaxMemory {
		return 0, fmt.Errorf("Requested Memory is grater than MaxMemory allowed by system administrator. Requested: %d. MaxMemory: %d", memory, MaxMemory)
	}
	return int64(memory), nil
}

func getEcsMemoryReservation(caps session.Caps) (int64, error) {
	memoryReservation, err := parseResourceCapability(caps.MemoryReservation, MinMemoryReservation, "MemoryReservation")
	if err != nil {
		return 0, err
	}
	if memoryReservation < MinMemoryReservation {
		fmt.Println("[WARN] Requested MemoryReservation lower than MinMemoryReservation. Using MinMemoryReservation as default. Requested:", memoryReservation, "MinMemoryReservation:", MinMemoryReservation)
		return int64(MinMemoryReservation), nil
	} else if memoryReservation > MaxMemoryReservation {
		return 0, fmt.Errorf("Requested MemoryReservation is grater than MaxMemoryReservation allowed by system administrator. Requested: %d. MaxMemory: %d", memoryReservation, MaxMemoryReservation)
	}
	return int64(memoryReservation), nil
}

func getEcsCpu(caps session.Caps) (int64, error) {
	cpu, err := parseResourceCapability(caps.Cpu, MinCpu, "Cpu")
	if err != nil {
		return 0, err
	}
	if cpu < MinCpu {
		fmt.Println("[WARN] Requested CPU lower than MinCpu. Using MinCpu as default. Requested:", cpu, "MinCpu:", MinCpu)
		return int64(MinCpu), nil
	} else if cpu > MaxCpu {
		return 0, fmt.Errorf("Requested CPU is grater than MaxCpu allowed by system administrator. Requested: %d. MaxCpu: %d", cpu, MaxCpu)
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

func RemoveTask(taskArn string) {
	log.WithField("taskARN", taskArn).Info("Removing task")
	sess, err := awsSession.NewSession(&aws.Config{Region: &AwsRegion, MaxRetries: &AwsRetry})
	if err != nil {
		log.WithError(err).WithField("taskARN", taskArn).Warn("Failed to stop task")
		return
	}
	svc := ecs.New(sess)

	stopTaskInput := &ecs.StopTaskInput{
		Cluster: &AwsCluster,
		Reason:  aws.String("Cancel"),
		Task:    aws.String(taskArn),
	}

	resultStopTask, err := svc.StopTask(stopTaskInput)
	if err != nil {
		//log.Printf("[FAILED_TO_STOP_TASK] [%s] [%v]", taskArn, err)
		log.WithError(err).WithField("taskARN", taskArn).Warn("Failed to stop task")
		return
	}
	taskDefinitionArn := *resultStopTask.Task.TaskDefinitionArn

	taskDeregisterInput := &ecs.DeregisterTaskDefinitionInput{
		TaskDefinition: aws.String(taskDefinitionArn),
	}
	resultTaskDeregister, err := svc.DeregisterTaskDefinition(taskDeregisterInput)
	if err != nil {
		//log.Printf("[FAILED_TO_DEREGISTER_TASK_DEFINITION] [%s] [%v]", taskDefinitionArn, err)
		log.WithError(err).WithField("taskDefinitionARN", taskDefinitionArn).Error("Failed to deregister task definition")
		return
	}
	//log.Printf("[TASK_DEFINITION_REMOVED] [%s]", *resultTaskDeregister.TaskDefinition.TaskDefinitionArn)
	log.WithField("taskDefinitionARN", *resultTaskDeregister.TaskDefinition.TaskDefinitionArn).Info("Task definition removed")
}

func GeneratePreSignedURL(key string) (string, error) {
	sess, err := awsSession.NewSession(&aws.Config{Region: &AwsRegion})
	if err != nil {
		return "", err
	}
	s3Svc := s3.New(sess)
	req, _ := s3Svc.GetObjectRequest(&s3.GetObjectInput{
		Bucket: &S3Bucket,
		Key:    &key,
	})
	urlStr, err := req.Presign(10 * time.Minute)
	if err != nil {
		return "", err
	}

	return urlStr, nil
}
