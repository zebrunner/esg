package service

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/aerokube/util"
	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/client"
	awsSession "github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/aws/aws-sdk-go/service/ecrpublic"
	"github.com/aws/aws-sdk-go/service/ecs"
	"github.com/aws/aws-sdk-go/service/s3"
	"github.com/google/uuid"
	log "github.com/sirupsen/logrus"
	"github.com/zebrunner/esg/config"
	"github.com/zebrunner/esg/selenium"
)

const (
	SeleniumPort   = 4444
	VncPort        = 5900
	DevtoolsPort   = 7070
	FileServerPort = 8080
	ClipboardPort  = 9090
)

var (
	AwsSess *awsSession.Session
)

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

func RunTask(ctx context.Context, conf selenium.ContainerConfiguration, family string, username string) (taskArn string, returnErr error) {
	svc := ecs.New(AwsSess)

	memory := conf.GetMemory()
	memoryReservation := conf.GetMemory()
	cpu := conf.GetCpu()

	browserContainerName := "browser"
	id := uuid.New().String()

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
					Value: aws.String(strconv.FormatBool(conf.EnableVnc)),
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

	// TODO: explicitly minimize errors range to wait only by well-knoen reasons aka RESOURCE:CPU etc
	// TODO: convert existing hard-coded 25 retries into the queue or provisioning timeout: https://github.com/zebrunner/esg/issues/72
	// [VD] "i" retry should be ~15 if instances can be started in 1 min and 25 if ~2 min
	var outputErr error
	for i := 0; i < 25; i++ {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		default:
		}
		// Trying to minimize random sleep this needs performance test. If it doesn't works return old sleep.
		sleep := time.Duration(rand.Intn(15)) * time.Second
		time.Sleep(sleep)
		var resultRunTask *ecs.RunTaskOutput
		resultRunTask, err := svc.RunTask(runTaskInput)
		// Not good solution but aws doesn't give a choice
		if err != nil && err.Error() == "ClientException: TaskDefinition not found." {
			return "", fmt.Errorf("Browser %s not found", family)
		}

		if err != nil {
			log.WithError(err).WithField("attempt", i).Debug("RunTask attempt failed.")
			outputErr = err
			continue
		}

		if len(resultRunTask.Failures) != 0 {
			log.WithFields(log.Fields{
				"attempt": i,
				"error":   *resultRunTask.Failures[0].Reason,
			}).Debug("Run task attempt failed. Response contains failures")
			outputErr = errors.New("response contains failures")
			continue
		}

		if len(resultRunTask.Tasks) == 0 {
			log.WithField("attempt", i).Debug("Run task attempt failed. Response doesn't contains tasks")
			outputErr = errors.New("response doesn't contains tasks")
			continue
		}

		// All is ok. We got task then we can return it.
		return *resultRunTask.Tasks[0].TaskArn, nil
	}

	return "", outputErr
}

func ConstDelay(t time.Duration) func(int) time.Duration {
	return func(attempt int) time.Duration {
		return t
	}
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

func GetStartedServiceInfo(conf selenium.ContainerConfiguration, taskArn string) (*StartedService, error) {
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

	ipAddress := *resultInstance.Reservations[0].Instances[0].PrivateIpAddress
	if config.UsePublicIp {
		ipAddress = *resultInstance.Reservations[0].Instances[0].PublicIpAddress
	}
	log.WithFields(log.Fields{
		"instanceIP": ipAddress,
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

	hostPort := getTaskHostPort(conf, ipAddress, &portConfig)
	log.WithField("hostPort", hostPort).Debug()
	log.WithField("VNCPort", hostPort.VNC).Debug("VNC")

	u := &url.URL{Scheme: "http", Host: hostPort.Selenium, Path: conf.Browser().Path}
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

func StartDriver(ctx context.Context, conf selenium.ContainerConfiguration, username string) (*StartedService, error) {
	svc := ecs.New(AwsSess)
	browser := conf.Browser().TaskDefinitionFamily()

	var outputErr error
out:
	for i := 0; i < config.RetryCount; i++ {
		select {
		case <-ctx.Done():
			break out
		default:
		}
		startTime := time.Now()
		taskArn, err := RunTask(ctx, conf, browser, username)
		log.WithField("latency", time.Since(startTime)).Info("RunTask delay")
		if err != nil {
			log.WithError(err).WithField("attempt", i).Error("Failed to run task")
			outputErr = fmt.Errorf("failed to start task. InternalError: %v", err)
			continue
		}

		taskId := strings.Split(taskArn, "/")[2]
		startTime = time.Now()
		err = waitUntilTaskIsRunning(ctx, svc, taskId, ConstDelay(6*time.Second), 25)
		log.WithField("latency", time.Since(startTime)).Info("WaitUntilTasksRunning delay")
		if err != nil {
			RemoveTask(taskArn)
			log.WithError(err).WithFields(log.Fields{
				"taskId":  taskId,
				"attempt": i,
			}).Error("Failed to wait task successfull state")
			outputErr = fmt.Errorf("failed to wait until task is running. InternalError: %v", err)
			continue
		}

		startTime = time.Now()
		sessionInfo, err := GetStartedServiceInfo(conf, taskArn)
		log.WithField("latency", time.Since(startTime)).Info("GetStartedServiceInfo delay")
		if err != nil {
			RemoveTask(taskArn)
			log.WithError(err).WithFields(log.Fields{
				"taskId":  taskId,
				"attempt": i,
			}).Error("Failed to get service info.")
			outputErr = fmt.Errorf("failed to get service info. InternalError: %v", err)
			continue
		}

		err = wait(ctx, sessionInfo.Url.String(), config.ServiceStartupTimeout)
		if err != nil {
			RemoveTask(taskArn)
			log.WithError(err).WithFields(log.Fields{
				"taskId":  taskId,
				"attempt": i,
			}).Error("Failed to wait browser response")
			outputErr = err
			continue
		}

		return sessionInfo, nil
	}

	return nil, outputErr
}

func waitUntilTaskIsRunning(ctx context.Context, svc *ecs.ECS, taskId string, sleepFn func(int) time.Duration, maxAttempts int) error {
	for i := 0; i < maxAttempts; i++ {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
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

	return errors.New("failed to wait successfull task status. Max attempt limit exceeded")
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

func getTaskHostPort(conf selenium.ContainerConfiguration, taskIP string, pc *ecsPortConfig) selenium.HostPort {
	containerIP := taskIP
	fn := func(containerPort int64) string {
		return containerIP + ":" + strconv.FormatInt(containerPort, 10)
	}

	hp := selenium.HostPort{
		Selenium:   fn(pc.SeleniumPort),
		Fileserver: fn(pc.FileserverPort),
		Clipboard:  fn(pc.ClipboardPort),
		Devtools:   fn(pc.DevtoolsPort),
	}

	if conf.EnableVnc {
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
