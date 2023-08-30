package service

import (
	"fmt"
	"time"

	"math/rand"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/client"
	"github.com/aws/aws-sdk-go/aws/credentials"
	awsSession "github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/autoscaling"
	"github.com/aws/aws-sdk-go/service/ecs"
	"github.com/aws/aws-sdk-go/service/s3"
	log "github.com/sirupsen/logrus"
	"github.com/zebrunner/esg/cachemaps/taskmap"
	"github.com/zebrunner/esg/config"
	"github.com/zebrunner/esg/environment"
	"github.com/zebrunner/esg/utils"
)

const (
	presignUrlTimeout = 15 * time.Minute
)

var (
	AwsSess *awsSession.Session
)

func InitAws() error {
	sess, err := awsSession.NewSession(&aws.Config{
		Region:     &config.Conf.AwsRegion,
		MaxRetries: &config.Conf.AwsRetry,
		Retryer: client.DefaultRetryer{
			MaxThrottleDelay: 60 * time.Second,
			MinThrottleDelay: 5 * time.Second,
		},
	})
	if err != nil {
		log.Fatal("failed to init aws session")
		return err
	}

	AwsSess = sess

	return nil
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
		if v.HostPath != "" {
			volumes = append(volumes, &ecs.Volume{
				Host: &ecs.HostVolumeProperties{
					SourcePath: aws.String(v.HostPath),
				},
				Name: aws.String(n),
			})
		} else {
			volumes = append(volumes, &ecs.Volume{
				DockerVolumeConfiguration: &ecs.DockerVolumeConfiguration{
					Driver: aws.String(v.Driver),
					Scope:  aws.String(v.Scope),
				},
				Name: aws.String(n),
			})
		}
	}

	input.Volumes = volumes

	resultTaskDefinition, err := utils.RetryThrottling(svc.RegisterTaskDefinition)(&input)
	if err != nil {
		return nil, fmt.Errorf("failed to create task definition: %v", err)
	}

	return resultTaskDefinition.TaskDefinition, nil
}

func ConstDelay(t time.Duration) func(int) time.Duration {
	return func(attempt int) time.Duration {
		return t
	}
}

func StopTaskForcibly(taskId string, stopReason taskmap.StoppedReason) error {
	svc := ecs.New(AwsSess)

	stopTaskInput := &ecs.StopTaskInput{
		Cluster: &config.Conf.AwsCluster,
		Reason:  aws.String(string(stopReason)),
		Task:    aws.String(taskId),
	}

	l := log.WithField(config.TaskIdKey, taskId)
	var err error
	var result *ecs.StopTaskOutput
	for i := 0; i < 5; i++ {
		l = l.WithField("retry", i)

		result, err = utils.RetryThrottling(svc.StopTask)(stopTaskInput)
		if err != nil {
			l.WithError(err).Debug("Failed to stop task")
			time.Sleep(time.Duration(rand.Intn(30)) * time.Second)
		} else {
			l.WithField("result", result).Trace("task stopped")
			l.Info("task stopped")
			return nil
		}
	}

	return err
}

func StopTask(taskId string, stopReason taskmap.StoppedReason) error {
	cachedTask, _ := taskmap.Find(taskId)
	if cachedTask == nil {
		return StopTaskForcibly(taskId, stopReason)
	}

	if cachedTask.Status == taskmap.TaskStopped || cachedTask.Status == taskmap.TaskPendingToStop {
		return fmt.Errorf("can't stop task that is stopped/pending to stop. Task status: %v", cachedTask.Status)
	}

	// Cache bakup on task stop fail
	cachedTaskBak := *cachedTask

	// Set pendingToStop status so no new StopTask() call for current task would be performed
	cachedTask.Status = taskmap.TaskPendingToStop
	taskmap.Write(cachedTask.TaskId, cachedTask, 0)

	err := StopTaskForcibly(cachedTask.TaskId, stopReason)
	if err != nil {
		taskmap.Write(cachedTask.TaskId, &cachedTaskBak, 0)
	} else {
		cachedTask.Status = taskmap.TaskStopped
		cachedTask.StopReason = stopReason
		taskmap.Write(cachedTask.TaskId, cachedTask, 10*time.Minute)
	}

	return err
}

func DescribeTask(taskArn string) (*ecs.DescribeTasksOutput, error) {
	svc := ecs.New(AwsSess)
	input := &ecs.DescribeTasksInput{
		Cluster: &config.Conf.AwsCluster,
		Tasks: []*string{
			aws.String(taskArn),
		},
	}

	result, err := utils.RetryThrottling(svc.DescribeTasks)(input)
	return result, err
}

func DescribeTasks(taskArns []string) ([]*ecs.Task, error) {
	svc := ecs.New(AwsSess)
	taskPages := paginate(taskArns, 100)
	resultArr := make([]*ecs.Task, 0)

	for _, tasks := range taskPages {
		time.Sleep(2 * time.Second)
		input := &ecs.DescribeTasksInput{
			Cluster: &config.Conf.AwsCluster,
			Tasks:   aws.StringSlice(tasks),
		}

		result, err := utils.RetryThrottling(svc.DescribeTasks)(input)
		if err != nil {
			return nil, err
		}
		resultArr = append(resultArr, result.Tasks...)
	}

	return resultArr, nil
}

func GeneratePreSignedURL(key string) (string, error) {
	conf := &config.Conf

	s3Session := AwsSess
	if conf.S3AwsAccessKeyID == "" && conf.S3AwsSecretAccessKey == "" && conf.S3Region != "" {
		// only s3 region is provided
		s3Session = awsSession.Must(awsSession.NewSession(&aws.Config{
			Region: &conf.S3Region,
		}))

	} else if conf.S3AwsAccessKeyID != "" && conf.S3AwsSecretAccessKey != "" && conf.S3Region != "" {
		creds := credentials.NewStaticCredentials(conf.S3AwsAccessKeyID, conf.S3AwsSecretAccessKey, "")
		s3Session = awsSession.Must(awsSession.NewSession(&aws.Config{
			Credentials: creds,
			Region:      &conf.S3Region,
		}))
	}

	//S3 connection information
	s3Svc := s3.New(s3Session)

	//ZEB-5145: ESG: return 404 when requested video/session or execution log is not available
	res, err := utils.RetryThrottling(s3Svc.ListObjectsV2)(&s3.ListObjectsV2Input{
		Bucket: &config.Conf.S3Bucket,
		Prefix: &key,
	})

	if err != nil {
		return "", err
	}
	if *res.KeyCount == 0 {
		err = fmt.Errorf("The specified key does not exist: " + key)
		return "", err
	}

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

func GetClusterTasks() ([]*ecs.Task, error) {
	svc := ecs.New(AwsSess)
	tasks := []*ecs.Task{}
	listTasksInput := &ecs.ListTasksInput{
		Cluster: &config.Conf.AwsCluster,
	}
	for {
		listTasksResult, err := utils.RetryThrottling(svc.ListTasks)(listTasksInput)
		if err != nil {
			return nil, err
		}
		if len(listTasksResult.TaskArns) == 0 {
			break
		}

		describeTasksInput := &ecs.DescribeTasksInput{
			Cluster: &config.Conf.AwsCluster,
			Tasks:   listTasksResult.TaskArns,
		}
		describeTasksResult, err := utils.RetryThrottling(svc.DescribeTasks)(describeTasksInput)
		if err != nil {
			log.WithError(err).Warn("Failed to get all tasks. Only partial results returned")
			break
		}
		tasks = append(tasks, describeTasksResult.Tasks...)

		if listTasksResult.NextToken == nil {
			break
		}
		listTasksInput = listTasksInput.SetNextToken(*listTasksResult.NextToken)
	}

	return tasks, nil
}

func GetClusterTasksArns() ([]*string, error) {
	svc := ecs.New(AwsSess)
	taskArns := make([]*string, 0)
	listTasksInput := &ecs.ListTasksInput{
		Cluster: &config.Conf.AwsCluster,
	}

	for {
		listTasksResult, err := utils.RetryThrottling(svc.ListTasks)(listTasksInput)
		if err != nil {
			return nil, err
		}
		if len(listTasksResult.TaskArns) == 0 {
			break
		}

		taskArns = append(taskArns, listTasksResult.TaskArns...)

		if listTasksResult.NextToken == nil {
			break
		}
		listTasksInput = listTasksInput.SetNextToken(*listTasksResult.NextToken)
	}

	return taskArns, nil
}

func ListContainerInstances(svc *ecs.ECS) ([]*string, error) {
	containerInstancesArns := make([]*string, 0)
	listContainerInstancesInput := ecs.ListContainerInstancesInput{
		Cluster: &config.Conf.AwsCluster,
	}
	for {
		listContainerInstancesResult, err := utils.RetryThrottling(svc.ListContainerInstances)(&listContainerInstancesInput)
		if err != nil && len(listContainerInstancesResult.ContainerInstanceArns) != 0 {
			return nil, err
		}

		if len(listContainerInstancesResult.ContainerInstanceArns) == 0 {
			break
		}

		containerInstancesArns = append(containerInstancesArns, listContainerInstancesResult.ContainerInstanceArns...)

		if listContainerInstancesResult.NextToken == nil {
			break
		}
		listContainerInstancesInput = *listContainerInstancesInput.SetNextToken(*listContainerInstancesResult.NextToken)
	}

	return containerInstancesArns, nil
}

func DescribeContainerInstances(containerInstanceIdPtrs []*string, svc *ecs.ECS) ([]*ecs.ContainerInstance, error) {
	pages := paginate(containerInstanceIdPtrs, 100)
	containerInstances := make([]*ecs.ContainerInstance, 0)

	for _, page := range pages {
		describeInput := ecs.DescribeContainerInstancesInput{
			Cluster:            &config.Conf.AwsCluster,
			ContainerInstances: page,
		}

		describeResult, err := utils.RetryThrottling(svc.DescribeContainerInstances)(&describeInput)
		if err != nil {
			log.WithField("describeResult", describeResult).WithField("error", err).Error("Failed to DescribeContainerInstances!")
			return nil, err
		}

		containerInstances = append(containerInstances, describeResult.ContainerInstances...)
	}

	return containerInstances, nil
}

func TerminateInstancesInASG(ec2InstanceIdPtrs []*string, decrementDesiredCapacity bool, autoscalingSvc *autoscaling.AutoScaling) error {
	for _, instanceId := range ec2InstanceIdPtrs {
		stopInstanceInput := autoscaling.TerminateInstanceInAutoScalingGroupInput{
			InstanceId:                     instanceId,
			ShouldDecrementDesiredCapacity: aws.Bool(decrementDesiredCapacity),
		}

		_, err := utils.RetryThrottling(autoscalingSvc.TerminateInstanceInAutoScalingGroup)(&stopInstanceInput)
		if err != nil {
			log.WithError(err).Error("Failed to terminate instance")
			return err
		}

		// as we terminating one by one
		time.Sleep(250 * time.Millisecond)
	}

	return nil
}
