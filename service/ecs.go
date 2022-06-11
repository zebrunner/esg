package service

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/client"
	awsSession "github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/aws/aws-sdk-go/service/ecrpublic"
	"github.com/aws/aws-sdk-go/service/ecs"
	"github.com/aws/aws-sdk-go/service/s3"
	log "github.com/sirupsen/logrus"
	"github.com/zebrunner/esg/config"
	"github.com/zebrunner/esg/environment"
)

const (
	browsersRepository = "659932254483"
	presignUrlTimeout  = 15 * time.Minute
)

var (
	AwsSess *awsSession.Session
)

func InitAws() (*awsSession.Session, error) {
	sess, err := awsSession.NewSession(&aws.Config{
		Region:     &config.Conf.AwsRegion,
		MaxRetries: &config.Conf.AwsRetry,
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
		Region:     aws.String("us-east-1"), // Hardcoded because ecr-public has only this region
		MaxRetries: &config.Conf.AwsRetry,
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

	for _, repository := range config.SupportedRepositories {
		input := ecrpublic.DescribeImagesInput{
			RegistryId:     aws.String(browsersRepository),
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

func CreateTaskDefinition(environment *environment.ExecutionEnvironment) (taskDefinition *ecs.TaskDefinition, err error) {
	svc := ecs.New(AwsSess)

	networkMode := "bridge"
	input := ecs.RegisterTaskDefinitionInput{
		NetworkMode:          &networkMode,
		ContainerDefinitions: environment.ContainerDefinitions(),
		Family:               &environment.TaskDefinitionFamily,
	}

	volumes := []*ecs.Volume{}
	for n, v := range environment.Volumes {
		volumes = append(volumes, &ecs.Volume{
			Host: &ecs.HostVolumeProperties{
				SourcePath: aws.String(v.HostPath),
			},
			Name: aws.String(n),
		})
	}
	input.Volumes = volumes

	resultTaskDefinition, err := svc.RegisterTaskDefinition(&input)
	if err != nil {
		return nil, fmt.Errorf("failed to create task definition: %v", err)
	}

	return resultTaskDefinition.TaskDefinition, nil
}

func RunTask(ctx context.Context, env *environment.ExecutionEnvironment) (taskArn string, returnErr error) {
	svc := ecs.New(AwsSess)

	runTaskInput := &ecs.RunTaskInput{
		Cluster:        &config.Conf.AwsCluster,
		TaskDefinition: &env.TaskDefinitionFamily,
		Overrides:      &ecs.TaskOverride{ContainerOverrides: env.ContainerOverrides()},
		PlacementStrategy: []*ecs.PlacementStrategy{
			{
				Field: aws.String("memory"),
				Type:  aws.String("binpack"),
			},
		},
	}

	// TODO: explicitly minimize errors range to wait only by well-known reasons aka RESOURCE:CPU etc
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
		sleep := time.Duration(rand.Intn(30)) * time.Second
		time.Sleep(sleep)
		var resultRunTask *ecs.RunTaskOutput
		resultRunTask, err := svc.RunTask(runTaskInput)
		// Not good solution but aws doesn't give a choice
		if err != nil && err.Error() == "ClientException: TaskDefinition not found." {
			return "", fmt.Errorf("image %s not found", env.TaskDefinitionFamily)
		}

		if err != nil {
			log.WithError(err).WithField("retry", i).Debug("Run task failed.")
			outputErr = err
			continue
		}

		if len(resultRunTask.Failures) != 0 {
			log.WithFields(log.Fields{
				"retry": i,
				"error": *resultRunTask.Failures[0].Reason,
			}).Debug("Run task failed. Response contains failures")
			outputErr = errors.New("response contains failures")
			continue
		}

		if len(resultRunTask.Tasks) == 0 {
			log.WithField("retry", i).Debug("Run task failed. Response doesn't contains tasks")
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
		Cluster: &config.Conf.AwsCluster,
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

func searchHostPort(task *ecs.Task, containerPort int64) (port int64, ok bool) {
	// #284: improve container host/port detection via searchHostPort
        time.Sleep(time.Duration(5) * time.Second)

	for _, container := range task.Containers {
		for _, networkBinding := range container.NetworkBindings {
			if *networkBinding.ContainerPort == containerPort {
				return *networkBinding.HostPort, true
			}
		}
	}

	return 0, false
}

func getTaskIp(task *ecs.Task) (string, error) {
	svc := ecs.New(AwsSess)
	containerInstanceArn := *task.ContainerInstanceArn

	containerInstanceId := strings.Split(containerInstanceArn, "/")[2]
	log.WithFields(log.Fields{"ContainerInstanceArn": containerInstanceArn}).Debug()

	containerInstanceInput := &ecs.DescribeContainerInstancesInput{
		Cluster: &config.Conf.AwsCluster,
		ContainerInstances: []*string{
			aws.String(containerInstanceId),
		},
	}
	resultContainerInstance, err := svc.DescribeContainerInstances(containerInstanceInput)
	if err != nil {
		return "", fmt.Errorf("failed to get container instance details. err=%v", err)
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
		return "", fmt.Errorf("failed to get instance details. error=%v", err)
	}

	ipAddress := *resultInstance.Reservations[0].Instances[0].PrivateIpAddress
	if config.Conf.UsePublicIp {
		ipAddress = *resultInstance.Reservations[0].Instances[0].PublicIpAddress
	}
	log.WithFields(log.Fields{"instanceIP": ipAddress}).Debug()
	return ipAddress, nil
}

func setEnvironmentNetwork(env *environment.ExecutionEnvironment, task *ecs.Task) error {
	for _, endpoint := range env.Network.Endpoints {
		hostPort, ok := searchHostPort(task, endpoint.Port)
		if !ok {
			return fmt.Errorf("host port not found. containerPort=%d", endpoint.Port)
		}
		endpoint.Port = hostPort
	}

	ip, err := getTaskIp(task)
	if err != nil {
		return err
	}
	env.Network.IP = ip
	return nil
}

func StartDriver(ctx context.Context, env *environment.ExecutionEnvironment) error {
	svc := ecs.New(AwsSess)

	var outputErr error
        startTime := time.Now()
out:
	for i := 0; i < 100; i++ { //TODO: 100 is almost unlimited but think about do while...
		l := log.WithField("attempt", i)
		select {
		case <-ctx.Done():
			outputErr = fmt.Errorf("failed to run task: Service startup timed out")
			break out
		default:
		}

		taskArn, err := RunTask(ctx, env)

		l.WithField("latency", time.Since(startTime)).Info("RunTask delay")
		if err != nil {
			l.WithError(err).WithField("attempt", i).WithField("latency", time.Since(startTime)).Warn("Failed to run task")
			outputErr = fmt.Errorf("failed to run task: %v", err)
			continue
		}

		taskId := strings.Split(taskArn, "/")[2]
		env.TaskId = taskId
		l = l.WithField("taskId", taskId)
		task, err := waitUntilTaskIsRunning(ctx, svc, taskId, ConstDelay(6*time.Second), 10)
		l.WithField("attempt", i).WithField("latency", time.Since(startTime)).Info("WaitUntilTasksRunning delay")
		if err != nil {
			RemoveTask(taskArn)
			l.WithField("attempt", i).WithField("latency", time.Since(startTime)).WithError(err).Warn("Failed to wait task RUNNING state")
			outputErr = fmt.Errorf("failed to wait for task RUNNING state: %v", err)
			continue
		}

		err = setEnvironmentNetwork(env, task)
		l.WithField("attempt", i).WithField("latency", time.Since(startTime)).Info("setEnvironmentNetwork delay")
		if err != nil {
			RemoveTask(taskArn)
			l.WithField("attempt", i).WithField("latency", time.Since(startTime)).WithError(err).Warn("Failed to get service info.")
			outputErr = fmt.Errorf("failed to get service info: %v", err)
			continue
		}

		url, ok := env.Network.GetUrl("healthcheck")
		if !ok {
			//TODO: [VD] if no healthcheck do we really want to retry? Maybe force abort?
			RemoveTask(taskArn)
			l.WithField("attempt", i).WithField("latency", time.Since(startTime)).Error("Driver healthcheck missed.")
			outputErr = fmt.Errorf("driver healthcheck missed")
			continue
		}

		err = wait(ctx, url.String(), config.Conf.SessionStartupTimeout)
		if err != nil {
			RemoveTask(taskArn)
			l.WithField("attempt", i).WithField("latency", time.Since(startTime)).WithError(err).Warn("Failed to wait driver healthcheck response")
			outputErr = err
			continue
		}

		return nil
	}

	return outputErr
}

func waitUntilTaskIsRunning(ctx context.Context, svc *ecs.ECS, taskId string, sleepFn func(int) time.Duration, maxAttempts int) (*ecs.Task, error) {
	for i := 0; i < maxAttempts; i++ {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}
		log := log.WithFields(log.Fields{
			"attempt": i,
			"taskId":  taskId,
		})

		describeTaskInput := &ecs.DescribeTasksInput{
			Cluster: &config.Conf.AwsCluster,
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
			return describeTaskResult.Tasks[0], nil
		}

		time.Sleep(sleepFn(i))
	}

	return nil, errors.New("failed to wait successfull task status.")
}

func GeneratePreSignedURL(key string) (string, error) {
	s3Svc := s3.New(AwsSess)
	req, _ := s3Svc.GetObjectRequest(&s3.GetObjectInput{
		Bucket: &config.Conf.S3Bucket,
		Key:    &key,
	})
	urlStr, err := req.Presign(presignUrlTimeout)
	if err != nil {
		return "", err
	}

	return urlStr, nil
}
