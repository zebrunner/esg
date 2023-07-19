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

func RegisterTask(ctx context.Context, env *environment.ExecutionEnvironment) (taskArn string, returnErr error) {
	svc := ecs.New(AwsSess)

	family := env.TaskDefinitionFamily
	//used Contains() as task definition family could be org-generic/dev-generic etc.
	if !strings.Contains(family, "generic") {
		tag, err := definitionmap.FindRevision(env.HashOvverideDefinition())
		if err != nil {
			return "", fmt.Errorf("image not found: '%s'", env.TaskDefinitionFamily)
		}
		family = fmt.Sprint(family, ":", tag)
	}
	l := log.WithField("family", family)

	runTaskInput := &ecs.RunTaskInput{
		Cluster:        &config.Conf.AwsCluster,
		TaskDefinition: &family,
		Overrides:      &ecs.TaskOverride{ContainerOverrides: env.ContainerOverrides()},
		PlacementStrategy: []*ecs.PlacementStrategy{
			{
				Field: aws.String("memory"),
				Type:  aws.String("binpack"),
			},
		},
	}
	log.WithField("runTaskInput", runTaskInput).Trace("Res runTaskInput")

	// TODO: explicitly minimize errors range to wait only by well-known reasons aka RESOURCE:CPU etc
	// TODO: convert existing hard-coded 25 retries into the queue or provisioning timeout: https://github.com/zebrunner/esg/issues/72
	// [VD] "i" retry should be ~15 if instances can be started in 1 min and 25 if ~2 min
	var outputErr error
	for i := 0; i < 25; i++ {

		l = l.WithField("retry", i)

		select {
		case <-ctx.Done():
			return "", ctx.Err()
		default:
		}
		// Random sleep to fix problems with parallel 100+ threads startup. Not applicable for generic tasks!
		//TODO: uncomment before release!
		/*		if env.TaskDefinitionFamily != "generic" {
					sleep := time.Duration(rand.Intn(30)) * time.Second
					time.Sleep(sleep)
				}
		*/

		var resultRunTask *ecs.RunTaskOutput
		resultRunTask, err := svc.RunTask(runTaskInput)
		// Not good solution but aws doesn't give a choice
		if err != nil && err.Error() == "ClientException: TaskDefinition not found." {
			return "", fmt.Errorf("image not found: '%s'", env.TaskDefinitionFamily)
		}

		sleepRateLimit := time.Duration(15 + rand.Intn(15))
		if err != nil &&
			(strings.Contains(err.Error(), "ThrottlingException: Rate exceeded") || err.Error() == "ClientException: Tasks provisioning capacity limit exceeded.") {
			time.Sleep(sleepRateLimit)
		}

		if err != nil {
			l.WithError(err).Debug("Run task failed.")
			outputErr = err
			continue
		}

		if len(resultRunTask.Failures) != 0 {
			l.WithField("error", *resultRunTask.Failures[0].Reason).Debug("Run task failed. Response contains failures")
			outputErr = errors.New("response contains failures")
			continue
		}

		if len(resultRunTask.Tasks) == 0 {
			l.Debug("Run task failed. Response doesn't contains tasks")
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

func setEnvironmentNetwork(ctx context.Context, env *environment.ExecutionEnvironment, task *ecs.Task) error {
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

func StartTask(ctx context.Context, env *environment.ExecutionEnvironment) (*taskmap.Task, error) {
	var outputErr error
	startTime := time.Now()
	// retry attempt counter
out:
	for i := 0; true; i++ {
		l := log.WithField("attempt", i)
		select {
		case <-ctx.Done():
			outputErr = fmt.Errorf("failed to run task: Service startup timed out")
			break out
		default:
		}

		taskArn, err := RegisterTask(ctx, env)

		if err != nil {
			outputErr = fmt.Errorf("failed to run task: %v", err)
			l.WithError(outputErr).WithField("latency", time.Since(startTime)).Warn()

			if strings.HasPrefix(err.Error(), "image not found: ") || strings.HasPrefix(err.Error(), "InvalidParameterException") { //#366 disable retries for InvalidParameterException
				break out
			}
			continue
		}

		// caching task as soon as possible
		cachedTask, err := taskmap.CreateEntity(strings.Split(taskArn, "/")[2], env)
		if err != nil {
			outputErr = fmt.Errorf("task not cached!: %v", err)
			l.WithError(outputErr).Warn()
			err := StopTask(cachedTask.ID, taskmap.TaskStartupFailure)
			if err != nil {
				l.WithError(err).Warn("Failed to stop task")
			}
			continue
		}
		l = l.WithField("_taskId", cachedTask.ID)

		if strings.Contains(env.TaskDefinitionFamily, "generic") {
			//TODO: remove HealthAt as only healthcheck integrated into the generic as well
			cachedTask.HealthAt = time.Now()
			taskmap.Write(cachedTask.ID, cachedTask, 0)
			l.Debug("do not wait for generic task startup.")
			return cachedTask, nil
		}

		l.Debug("Waiting for the task to start")
		req := taskWaiter.waitFor(ctx, taskArn)
		select {
		case <-req.ctx.Done():
			// don't close chans from receiver side
			// https://go.dev/tour/concurrency/4#:~:text=Note%3A%20Only%20the%20sender%20should,to%20terminate%20a%20range%20loop.
			l.WithField("latency", time.Since(startTime)).Warn("failed to wait until task is running. context deadline")
		case err := <-req.errorChan:
			outputErr = fmt.Errorf("failed to wait until Task is running and healthy!: %v", err)
			l.WithField("latency", time.Since(startTime)).WithError(outputErr).Warn()
		case task := <-req.responseChan:
			// timediff between HealthAt (current time) and task.startedAt should be cut during resources tracking to bill only actual (net) time
			cachedTask.HealthAt = time.Now()
			taskmap.Write(cachedTask.ID, cachedTask, 0)
			l.Info("healthcheck latency: ", time.Since(startTime))

			err = setEnvironmentNetwork(ctx, env, task)
			l.Debug("setEnvironmentNetwork latency: ", time.Since(startTime))
			if err != nil {
				outputErr = fmt.Errorf("failed to get network info: %v", err)
				l.WithField("latency", time.Since(startTime)).WithError(outputErr).Warn()
			} else {
				cachedTask.Status = taskmap.TaskActive
				err = taskmap.Write(cachedTask.ID, cachedTask, 0)
				if err != nil {
					l.WithError(fmt.Errorf("task not recached after network set!: %v", err))
				}
				return cachedTask, nil
			}
		}
		// will be called only for unsuccess task startup
		// as on success startup we return from func in switch select
		err = StopTask(cachedTask.ID, taskmap.TaskStartupFailure)
		if err != nil {
			l.WithError(err).Warn("Failed to stop task")
		}
	}

	return nil, outputErr
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
