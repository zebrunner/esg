package service

import (
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
	"github.com/zebrunner/esg/cachemaps"
	"github.com/zebrunner/esg/cachemaps/mapper"
	"github.com/zebrunner/esg/config"

	"github.com/zebrunner/esg/utils"
)

const (
	presignUrlTimeout = 15 * time.Minute
)

var (
	AwsSess          *awsSession.Session
	progressivePause utils.ProgressivePause
)

func init() {
	sess, err := InitAws()
	if err != nil {
		log.Fatal("failed to init aws session")
	}
	AwsSess = sess
	progressivePause = utils.CreateProgressivePause(0, 350)
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

func CreateTaskDefinition(definitions []*ecs.ContainerDefinition, volumes []*ecs.Volume, taskDefinitionFamily string, taskRoleArn string) (*ecs.TaskDefinition, error) {
	svc := ecs.New(AwsSess)

	networkMode := "awsvpc"
	input := ecs.RegisterTaskDefinitionInput{
		NetworkMode:          &networkMode,
		ContainerDefinitions: definitions,
		Volumes:              volumes,
		Family:               &taskDefinitionFamily,
		TaskRoleArn:          &taskRoleArn,
	}

	var err error
	i := 0
	for ; i < 10; i++ {
		time.Sleep(progressivePause.GetPause())

		var resultTaskDefinition *ecs.RegisterTaskDefinitionOutput
		resultTaskDefinition, err = utils.RetryThrottling(svc.RegisterTaskDefinition)(&input)

		if err != nil {
			log.WithField("retry", i).WithError(err).Warn("failed to create task definition")
			if !strings.Contains(err.Error(), "ClientException") {
				return nil, err
			}
		} else {
			return resultTaskDefinition.TaskDefinition, nil
		}
	}
	return nil, fmt.Errorf("failed to create task definition in %v retries: %v", i, err)
}

func ConstDelay(t time.Duration) func(int) time.Duration {
	return func(attempt int) time.Duration {
		return t
	}
}

func StopTaskForcibly(taskId string, stopReason mapper.StoppedReason) error {
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

func StopTask(mapperEntity mapper.Mapper, stopReason mapper.StoppedReason) error {
	l := log.WithField(config.TaskIdKey, mapperEntity.TaskId).WithField(config.RouterUUID, mapperEntity.RouterUUID)

	err := StopTaskForcibly(mapperEntity.TaskId, stopReason)
	if err != nil {
		l.WithError(err).Error("Failed to stop task!")
		return err
	}

	mapperEntity.Status = mapper.Stopped
	mapperEntity.StopReason = stopReason
	setsToDettach := []cachemaps.SetType{}
	if mapperEntity.SessionID != "" {
		setsToDettach = append(setsToDettach, cachemaps.SESSION)
	}

	err = mapper.WritedByWorker(&mapperEntity, nil, setsToDettach, 10*time.Minute)
	if err != nil {
		l.WithError(err).Error("Failed to update task's cache!")
		return err
	}

	return nil
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
	taskPages := utils.Paginate(taskArns, 100)
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
