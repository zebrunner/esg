package service

import (
	"context"
	"errors"
	"fmt"
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
	"math/rand"
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

func ListBrowsers() ([]string, error) {
	sess, err := awsSession.NewSession(&aws.Config{
		Region:     aws.String("us-east-1"), // Hardcoded because ecr-public has only this region
		MaxRetries: &config.Conf.AwsRetry,
		Retryer: client.DefaultRetryer{
			MaxThrottleDelay: 60 * time.Second,
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
			log.Debug("image: ", image)
			for _, tag := range image.ImageTags {
				images = append(images, repository + ":" +*tag)
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
                if v.HostPath != "" {
                        volumes = append(volumes, &ecs.Volume{
                                Host: &ecs.HostVolumeProperties{
                                        SourcePath: aws.String(v.HostPath),
                                },
                                Name: aws.String(n),
                        })
                } else {
                        volumes = append(volumes, &ecs.Volume{
                                DockerVolumeConfiguration: &ecs.DockerVolumeConfiguration {
                                        Driver: aws.String(v.Driver),
                                        Scope: aws.String(v.Scope),
                                },
                                Name: aws.String(n),
                        })
                }
        }

	input.Volumes = volumes

	resultTaskDefinition, err := svc.RegisterTaskDefinition(&input)
	if err != nil {
		return nil, fmt.Errorf("failed to create task definition: %v", err)
	}

	return resultTaskDefinition.TaskDefinition, nil
}


func CreateGenericTaskDefinition(environment *environment.ExecutionEnvironment) (taskDefinition *ecs.TaskDefinition, err error) {
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
                                DockerVolumeConfiguration: &ecs.DockerVolumeConfiguration {
                                        Driver: aws.String(v.Driver),
                                        Scope: aws.String(v.Scope),
                                },
                                Name: aws.String(n),
                        })
                }
        }
        input.Volumes = volumes

        resultTaskDefinition, err := svc.RegisterTaskDefinition(&input)
        //log.WithField("resultTaskDefinition", resultTaskDefinition).Info("Res TaskDefinition")
        if err != nil {
                return nil, fmt.Errorf("failed to create task definition: %v", err)
        }

        return resultTaskDefinition.TaskDefinition, nil
}


func RegisterTask(ctx context.Context, env *environment.ExecutionEnvironment) (taskArn string, returnErr error) {
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
        log.WithField("runTaskInput", runTaskInput).Debug("Res runTaskInput")

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
		// Random sleep to fix problems with parallel 100+ threads startup. Not applicable got generic and cypress tasks!
                if env.TaskDefinitionFamily != "generic" && !strings.HasPrefix(env.TaskDefinitionFamily, "cypress") {
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

	stopTaskInput := &ecs.StopTaskInput{
		Cluster: &config.Conf.AwsCluster,
		Reason:  aws.String("Cancel"),
		Task:    aws.String(taskArn),
	}

	i := 0
        result, err := svc.StopTask(stopTaskInput)
	for i < 25 {
        	if err == nil {      // the condition stops matching
			log.WithField("id", taskArn).WithField("result", result).Debug("    task stopped")
			log.WithField("id", taskArn).Info("    task stopped") //spaces in the beginning for #390
                        // break out of the loop
                	break
        	} else {
			time.Sleep(time.Duration(rand.Intn(30)) * time.Second)
			i = i + 1
			log.WithError(err).WithField("retry", i).Debug("Failed to stop task")
	                result, err = svc.StopTask(stopTaskInput)
		}
	}

	if (err != nil) {
		log.WithError(err).WithField("retry", i).Error("Failed to stop task")
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

        result, err := svc.DescribeTasks(input)
        return result, err
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
	log.WithFields(log.Fields{"instanceIP": ipAddress}).Debug()
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

        //TODO: register new execution timeout (24hrs?)
        ctxRunner, _ := context.WithTimeout(context.Background(), 24*time.Hour)

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
			outputErr = fmt.Errorf("failed to run task: ", err)
			if strings.HasPrefix(err.Error(), "image not found: ") || strings.HasPrefix(err.Error(), "InvalidParameterException") { //#366 disable retries for InvalidParameterException
				break out
			}
			continue
		}

		taskId := strings.Split(taskArn, "/")[2]
		env.TaskId = taskId
		l = l.WithField("TaskId", taskId)

	        if env.TaskDefinitionFamily == "generic" || strings.HasPrefix(env.TaskDefinitionFamily, "cypress") {
	                //register runner taskId to track resources
        	        taskWaiter.waitFor(ctxRunner, taskArn, false)

			// do not wait for healtchcheck in generic and cypress tasks
			outputErr = nil
			return outputErr
		}

		req := taskWaiter.waitFor(ctx, taskArn, true) //for driver/browser sessions waitFor healthcheck state verification
		select {
		case err := <-req.errorChan:
			StopTask(taskArn)
			l.WithField("attempt", i).WithField("latency", time.Since(startTime)).WithError(err).Warn("Failed to wait until Task is running and healthy")
			outputErr = err
			continue
		case task := <-req.responseChan:
			err = setEnvironmentNetwork(env, task)
			l.WithField("attempt", i).Debug("setEnvironmentNetwork latency: ", time.Since(startTime))
			if err != nil {
				StopTask(taskArn)
				l.WithField("attempt", i).WithField("latency", time.Since(startTime)).WithError(err).Warn("Failed to get service info.")
				outputErr = fmt.Errorf("failed to get service info: %v", err)
				continue
			}
                        //re-register runner taskId to track browser resources till StoppedAt
                        taskWaiter.waitFor(ctxRunner, taskArn, false)

			outputErr = nil
			return outputErr
		case <-req.ctx.Done():
			outputErr = errors.New("failed to wait until task is running. context deadline")
			StopTask(taskArn)
			l.Error("TODO: need stopWait here?")
			taskWaiter.stopWait(taskArn)
			l.WithField("attempt", i).WithField("latency", time.Since(startTime)).WithError(err).Warn("failed to wait until task is running")
		}
	}

	return outputErr
}

func GeneratePreSignedURL(key string) (string, error) {
	s3Svc := s3.New(AwsSess)

	//ZEB-5145: ESG: return 404 when requested video/session or execution log is not available
	res, err := s3Svc.ListObjectsV2(&s3.ListObjectsV2Input{
		Bucket: &config.Conf.S3Bucket,
		Prefix: &key,
	})

	if err != nil {
		return "", err
	}
	if (*res.KeyCount == 0) {
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
