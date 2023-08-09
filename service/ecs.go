package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"math/rand"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/client"
	"github.com/aws/aws-sdk-go/aws/credentials"
	awsSession "github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/ecs"
	"github.com/aws/aws-sdk-go/service/s3"
	log "github.com/sirupsen/logrus"
	"github.com/zebrunner/esg/cachemaps/definitionmap"
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

func init() {
	sess, err := InitAws()
	if err != nil {
		log.Fatal("failed to init aws session")
	}
	AwsSess = sess
}

func InitAws() (*awsSession.Session, error) {
	sess, err := awsSession.NewSession(&aws.Config{
		Region:     &config.Conf.AwsRegion,
		MaxRetries: &config.Conf.AwsRetry,
		Retryer: client.DefaultRetryer{
			MaxThrottleDelay: 60 * time.Second,
			MinThrottleDelay: 5 * time.Second,
		},
	})
	if err != nil {
		return nil, err
	}

	return sess, nil
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

func StopTask(taskId string, stopReason taskmap.StoppedReason) error {
	svc := ecs.New(AwsSess)

	stopTaskInput := &ecs.StopTaskInput{
		Cluster: &config.Conf.AwsCluster,
		Reason:  aws.String(string(stopReason)),
		Task:    aws.String(taskId),
	}

	cachedTask, _ := taskmap.Find(taskId)
	var oldTaskStatus taskmap.TaskStatus
	if cachedTask != nil {
		if cachedTask.Status == taskmap.TaskStopped || cachedTask.Status == taskmap.TaskPendingToStop {
			err := errors.New("StopTask() call for stopped/pending to stop task")
			return err
		} else {
			// Set pendingToStop status so no new StopTask() call for current task would be performed
			oldTaskStatus = cachedTask.Status
			cachedTask.Status = taskmap.TaskPendingToStop
			taskmap.Write(taskId, cachedTask, 0)
		}
	}

	l := log.WithField("_taskId", taskId)
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

			// Set stopped status and expiration time 10 minutes to be able to track task's usage
			if cachedTask != nil {
				cachedTask.Status = taskmap.TaskStopped
				cachedTask.StopReason = stopReason
				taskmap.Write(taskId, cachedTask, 10*time.Minute)
			}

			// break out of the loop
			break
		}
	}

	if err != nil {
		l.WithError(err).Error("Failed to stop task")
		// revert old status because of a stop failure
		if cachedTask != nil {
			cachedTask.Status = oldTaskStatus
			taskmap.Write(taskId, cachedTask, 0)
		}
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

func searchHostPort(task *ecs.Task, containerPort int64) (port int64, ok bool) {
	for _, container := range task.Containers {
		for _, networkBinding := range container.NetworkBindings {
			if *networkBinding.ContainerPort == containerPort {
				return *networkBinding.HostPort, true
			}
		}
	}

	return 0, false
}

func getTaskIp(ctx context.Context, task *ecs.Task) (string, error) {
	var ipAddress string

	req := instanceWorker.waitForInstance(ctx, task)
	select {
	case err := <-req.errorChan:
		log.WithError(err).Warn("Failed to get ip from instance")
		return "", err
	case instance := <-req.responseChan:
		if config.Conf.UsePublicIp {
			ipAddress = *instance.PublicIpAddress
		} else {
			ipAddress = *instance.PrivateIpAddress
		}
	case <-req.ctx.Done():
		return "", errors.New("failed to wait until ec2 instance is ready to run. context deadline")
	}

	log.WithField("instanceIP", ipAddress).Debug()
	return ipAddress, nil
}

func SetEnvironmentNetwork(ctx context.Context, env *environment.ExecutionEnvironment, task *ecs.Task) error {
	for _, endpoint := range env.Network.Endpoints {
		hostPort, ok := searchHostPort(task, endpoint.ContainerPort)
		if !ok {
			return fmt.Errorf("host port not found. containerPort=%d", endpoint.ContainerPort)
		}
		endpoint.HostPort = hostPort
	}

	ip, err := getTaskIp(ctx, task)
	if err != nil {
		return err
	}
	env.Network.IP = ip
	return nil
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
		err = errors.New("The specified key does not exist: " + key)
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
