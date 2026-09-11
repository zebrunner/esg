package service

import (
	"context"
	"fmt"
	"math/rand"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ecs"
	ecsTypes "github.com/aws/aws-sdk-go-v2/service/ecs/types"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	log "github.com/sirupsen/logrus"
	"github.com/zebrunner/esg/cachemaps"
	"github.com/zebrunner/esg/cachemaps/mapper"
	"github.com/zebrunner/esg/config"
	"github.com/zebrunner/esg/utils"
)

const (
	presignUrlTimeout = 15 * time.Minute

	// Grace period a stopped session stays readable so callers get its stop reason instead of a miss.
	stoppedSessionTTL = 10 * time.Minute
)

var (
	progressivePause utils.ProgressivePause
)

func init() {
	progressivePause = utils.CreateProgressivePause(0, 350)
}

func CreateTaskDefinition(ctx context.Context, definitions []ecsTypes.ContainerDefinition, volumes []ecsTypes.Volume, taskDefinitionFamily string, taskRoleArn string) (*ecsTypes.TaskDefinition, error) {
	ctx, cancel := context.WithTimeout(ctx, AwsCallTimeout)
	defer cancel()

	svc := ecs.NewFromConfig(AwsCfg)

	input := &ecs.RegisterTaskDefinitionInput{
		NetworkMode:          ecsTypes.NetworkModeBridge,
		ContainerDefinitions: definitions,
		Volumes:              volumes,
		Family:               aws.String(taskDefinitionFamily),
		TaskRoleArn:          aws.String(taskRoleArn),
	}

	var err error
	i := 0
	for ; i < 10; i++ {
		time.Sleep(progressivePause.GetPause())

		var resultTaskDefinition *ecs.RegisterTaskDefinitionOutput
		resultTaskDefinition, err = svc.RegisterTaskDefinition(ctx, input)

		if err != nil {
			log.WithField("retry", i).WithError(err).Warn("failed to create task definition")
			if !strings.Contains(err.Error(), "ClientException") {
				return nil, err
			}
		} else {
			taskDef := resultTaskDefinition.TaskDefinition
			familyRevision := fmt.Sprintf("%s:%d", taskDefinitionFamily, taskDef.Revision)
			if tags := BuildTaskDefinitionTags(familyRevision); len(tags) > 0 {
				_, tagErr := svc.TagResource(ctx, &ecs.TagResourceInput{
					ResourceArn: taskDef.TaskDefinitionArn,
					Tags:        tags,
				})
				if tagErr != nil {
					log.WithError(tagErr).Warn("failed to tag task definition")
				}
			}
			return taskDef, nil
		}
	}
	return nil, fmt.Errorf("failed to create task definition in %v retries: %v", i, err)
}

func BuildRunTaskTags() []ecsTypes.Tag {
	return buildTags(config.Conf.EcsTaskTags)
}

func BuildTaskDefinitionTags(familyRevision string) []ecsTypes.Tag {
	tagMap := config.Conf.EcsTaskDefinitionTags
	if len(tagMap) == 0 {
		return nil
	}
	tags := buildTags(tagMap)
	if _, hasName := tagMap["Name"]; !hasName {
		tags = append(tags, ecsTypes.Tag{
			Key:   aws.String("Name"),
			Value: aws.String(familyRevision),
		})
	}
	return tags
}

func buildTags(tagMap utils.TagMap) []ecsTypes.Tag {
	if len(tagMap) == 0 {
		return nil
	}
	tags := make([]ecsTypes.Tag, 0, len(tagMap))
	for k, v := range tagMap {
		tags = append(tags, ecsTypes.Tag{
			Key:   aws.String(k),
			Value: aws.String(v),
		})
	}
	return tags
}

func ConstDelay(t time.Duration) func(int) time.Duration {
	return func(attempt int) time.Duration {
		return t
	}
}

func StopTaskForcibly(ctx context.Context, taskId string, stopReason mapper.StoppedReason) error {
	ctx, cancel := context.WithTimeout(ctx, AwsCallTimeout)
	defer cancel()

	svc := ecs.NewFromConfig(AwsCfg)

	stopTaskInput := &ecs.StopTaskInput{
		Cluster: aws.String(config.Conf.AwsCluster),
		Reason:  aws.String(string(stopReason)),
		Task:    aws.String(taskId),
	}

	l := log.WithField(config.TaskIdKey, taskId)
	var err error
	var result *ecs.StopTaskOutput
	for i := 0; i < 5; i++ {
		l = l.WithField("retry", i)

		result, err = svc.StopTask(ctx, stopTaskInput)
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

func StopTask(ctx context.Context, mapperEntity mapper.Mapper, stopReason mapper.StoppedReason) error {
	l := log.WithField(config.TaskIdKey, mapperEntity.TaskId).WithField(config.RouterUUID, mapperEntity.RouterUUID)

	err := StopTaskForcibly(ctx, mapperEntity.TaskId, stopReason)
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

	err = mapper.WritedByWorker(&mapperEntity, nil, setsToDettach, stoppedSessionTTL)
	if err != nil {
		l.WithError(err).Error("Failed to update task's cache!")
		return err
	}

	// Every id handed out for this session must stop resolving when the session it points at does.
	if err := mapper.ExpireChildren(mapperEntity.Children, stoppedSessionTTL); err != nil {
		l.WithError(err).Warn("Failed to expire child session ids")
	}

	return nil
}

func DescribeTask(ctx context.Context, taskArn string) (*ecs.DescribeTasksOutput, error) {
	svc := ecs.NewFromConfig(AwsCfg)
	input := &ecs.DescribeTasksInput{
		Cluster: aws.String(config.Conf.AwsCluster),
		Tasks:   []string{taskArn},
	}

	return svc.DescribeTasks(ctx, input)
}

func DescribeTasks(ctx context.Context, taskArns []string) ([]ecsTypes.Task, error) {
	svc := ecs.NewFromConfig(AwsCfg)
	taskPages := utils.Paginate(taskArns, 100)
	resultArr := make([]ecsTypes.Task, 0)

	for _, tasks := range taskPages {
		time.Sleep(2 * time.Second)
		input := &ecs.DescribeTasksInput{
			Cluster: aws.String(config.Conf.AwsCluster),
			Tasks:   tasks,
		}

		result, err := svc.DescribeTasks(ctx, input)
		if err != nil {
			return nil, err
		}
		resultArr = append(resultArr, result.Tasks...)
	}

	return resultArr, nil
}

func GeneratePreSignedURL(ctx context.Context, key string) (string, error) {
	s3Cfg, err := GetS3Config(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to get S3 config: %w", err)
	}

	s3Svc := s3.NewFromConfig(s3Cfg)

	res, err := s3Svc.ListObjectsV2(ctx, &s3.ListObjectsV2Input{
		Bucket: aws.String(config.Conf.S3Bucket),
		Prefix: aws.String(key),
	})
	if err != nil {
		return "", err
	}
	if aws.ToInt32(res.KeyCount) == 0 {
		return "", fmt.Errorf("the specified key does not exist: %s", key)
	}

	presignClient := s3.NewPresignClient(s3Svc)

	presignedReq, err := presignClient.PresignGetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(config.Conf.S3Bucket),
		Key:    aws.String(key),
	}, s3.WithPresignExpires(presignUrlTimeout))
	if err != nil {
		return "", err
	}

	return presignedReq.URL, nil
}
