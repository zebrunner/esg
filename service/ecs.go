package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"os"

	"math/rand"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/client"
	"github.com/aws/aws-sdk-go/aws/credentials"
	awsSession "github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/aws/aws-sdk-go/service/ecs"
	"github.com/aws/aws-sdk-go/service/s3"
	log "github.com/sirupsen/logrus"
	"github.com/zebrunner/esg/config"
	"github.com/zebrunner/esg/environment"
	sessionmap "github.com/zebrunner/esg/sessinonmap"
	"github.com/zebrunner/esg/utils"
)

const (
	browsersRepository = "659932254483"
	presignUrlTimeout  = 15 * time.Minute
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

func getFamily(name string) (string) {
        zbrEnv := os.Getenv("ZEBRUNNER_ENV")
	if zbrEnv != "" {
		name = zbrEnv + "-" + name
		log.Debug("name: ", name)
	}
	return name
}

func CreateTaskDefinition(environment *environment.ExecutionEnvironment) (taskDefinition *ecs.TaskDefinition, err error) {
	svc := ecs.New(AwsSess)

	family := getFamily(environment.TaskDefinitionFamily)

	networkMode := "bridge"
	input := ecs.RegisterTaskDefinitionInput{
		NetworkMode:          &networkMode,
		ContainerDefinitions: environment.ContainerDefinitions(),
		Family:               &family,
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

	family := getFamily(env.TaskDefinitionFamily)
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

		l := log.WithField("retry", i)

		select {
		case <-ctx.Done():
			return "", ctx.Err()
		default:
		}
		// Random sleep to fix problems with parallel 100+ threads startup. Not applicable for generic tasks!
		if env.TaskDefinitionFamily != "generic" {
			sleep := time.Duration(rand.Intn(30)) * time.Second
			time.Sleep(sleep)
		}

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

func StopTask(taskId string, stopReason sessionmap.StoppedReason) (*ecs.StopTaskOutput, error) {
	svc := ecs.New(AwsSess)

	stopTaskInput := &ecs.StopTaskInput{
		Cluster: &config.Conf.AwsCluster,
		Reason:  aws.String("Cancel"),
		Task:    aws.String(taskId),
	}

	l := log.WithField("_taskId", taskId)

	session, _ := sessionmap.Find(taskId, true)
	var oldSessStatus sessionmap.SessionStatus
	if session != nil {
		if session.Status == sessionmap.SessionStopped || session.Status == sessionmap.SessionPendingToStop {
			err := errors.New("StopTask() call for already stopped task")
			return nil, err
		} else {
			// Set pendingToStop status so no new StopTask() call for current task would be performed
			oldSessStatus = session.Status
			session.Status = sessionmap.SessionPendingToStop
			sessionmap.Write(taskId, session, 0)
		}
	}

	i := 0
	result, err := svc.StopTask(stopTaskInput)
	for i < 25 {
		l := l.WithField("retry", i)
		if err == nil { // the condition stops matching
			l.WithField("result", result).Trace("task stopped")
			l.Info("task stopped")
			if session != nil {
				// Set stopped status and expiration time 10 minutes to be able to track task's usage
				session.Status = sessionmap.SessionStopped
				session.StopReason = stopReason
				sessionmap.Write(taskId, session, 10*time.Minute)
			}
			// break out of the loop
			break
		} else {
			time.Sleep(time.Duration(rand.Intn(30)) * time.Second)
			i = i + 1
			l.WithError(err).Debug("Failed to stop task")
			result, err = svc.StopTask(stopTaskInput)
		}
	}

	if err != nil {
		l.WithError(err).Error("Failed to stop task")
		// revert old status because of a stop failure
		if session != nil {
			session.Status = oldSessStatus
			sessionmap.Write(taskId, session, 0)
		}
	}

	return result, err
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

func getTaskIp(task *ecs.Task) (string, error) {
	containerInstanceArn := *task.ContainerInstanceArn
	// TODO: use better wait mechanism
	var ec2Instance *ec2.Instance
	for i := 0; i < 6; i++ {
		instance, ok := instanceWorker.getInstanceByContainerInstance(containerInstanceArn)
		if ok {
			ec2Instance = instance
			break
		}
		time.Sleep(10 * time.Second)
	}

	if ec2Instance == nil {
		return "", fmt.Errorf("instance with id: %s not found", containerInstanceArn)
	}
	// if !ok {
	// 	return "", fmt.Errorf("instance with id: %s not found", containerInstanceArn)
	// }

	ipAddress := *ec2Instance.PrivateIpAddress
	if config.Conf.UsePublicIp {
		ipAddress = *ec2Instance.PublicIpAddress
	}
	log.WithField("instanceIP", ipAddress).Debug()
	return ipAddress, nil
}

func setEnvironmentNetwork(env *environment.ExecutionEnvironment, task *ecs.Task) error {
	for _, endpoint := range env.Network.Endpoints {
		hostPort, ok := searchHostPort(task, endpoint.ContainerPort)
		if !ok {
			return fmt.Errorf("host port not found. containerPort=%d", endpoint.ContainerPort)
		}
		endpoint.HostPort = hostPort
	}

	ip, err := getTaskIp(task)
	if err != nil {
		return err
	}
	env.Network.IP = ip
	return nil
}

func StartTask(ctx context.Context, env *environment.ExecutionEnvironment) error {
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

		taskArn, err := RegisterTask(ctx, env)

		if err != nil {
			l.WithError(err).WithField("attempt", i).WithField("latency", time.Since(startTime)).Warn("Failed to run task")
			outputErr = fmt.Errorf("failed to run task: %v", err)
			if strings.HasPrefix(err.Error(), "image not found: ") || strings.HasPrefix(err.Error(), "InvalidParameterException") { //#366 disable retries for InvalidParameterException
				break out
			}
			continue
		}

		taskId := strings.Split(taskArn, "/")[2]
		env.TaskId = taskId
		l = l.WithField("_taskId", taskId)
		if env.TaskDefinitionFamily == "generic" {
			l.Debug("do not wait for generic task startup.")
			outputErr = nil
			return outputErr
		}

		req := taskWaiter.waitFor(ctx, taskArn)
		select {
		case err := <-req.errorChan:
			StopTask(taskId, sessionmap.SessionStartupFailure)
			l.WithField("latency", time.Since(startTime)).WithError(err).Warn("Failed to wait until Task is running and healthy")
			outputErr = err
			continue
		case task := <-req.responseChan:
			err = setEnvironmentNetwork(env, task)
			l.Debug("setEnvironmentNetwork latency: ", time.Since(startTime))
			if err != nil {
				StopTask(taskId, sessionmap.SessionStartupFailure)
				l.WithField("latency", time.Since(startTime)).WithError(err).Error("Failed to get service info.")
				outputErr = fmt.Errorf("failed to get service info: %v", err)
				continue
			}

			outputErr = nil
			return outputErr
		case <-req.ctx.Done():
			outputErr = errors.New("failed to wait until task is running. context deadline")
			StopTask(taskId, sessionmap.SessionStartupFailure)
			taskWaiter.stopWait(taskArn)
			l.WithField("latency", time.Since(startTime)).WithError(err).Warn("failed to wait until task is running")
		}
	}

	return outputErr
}

func GeneratePreSignedURL(key string) (string, error) {
        conf := &config.Conf

	s3Session := AwsSess
	if conf.S3AwsAccessKeyID == "" && conf.S3AwsSecretAccessKey == "" && conf.S3Region != "" {
		// only s3 region is provided
                s3Session = awsSession.Must(awsSession.NewSession(&aws.Config{
                        Region:      &conf.S3Region,
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
