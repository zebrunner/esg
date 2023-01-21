package main

import (
	"context"
	"flag"
	"io/ioutil"
	"strings"
	"sync"
	"time"
	"math"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/ecs"
	"github.com/zebrunner/esg/capabilities"
	"github.com/zebrunner/esg/environment"
	"github.com/zebrunner/esg/service"
	sessionmap "github.com/zebrunner/esg/sessinonmap"

	awsSession "github.com/aws/aws-sdk-go/aws/session"
	log "github.com/sirupsen/logrus"
	"github.com/zebrunner/esg/config"

        "github.com/zebrunner/esg/zebrunner"
)

var (
	wg                  sync.WaitGroup
	enableFastScaleDown bool
)

func init() {
	flag.BoolVar(&enableFastScaleDown, "enable-fast-scale-down", true, "Enable ESG scale down option")
}

func ClearTasks() {

        //TODO: #427 move zombie tasks handler to new ClearTasks method
        session, err := awsSession.NewSession(&aws.Config{Region: &config.Conf.AwsRegion, MaxRetries: &config.Conf.AwsRetry})
        if err != nil {
                log.WithError(err).Error("Failed to create AWS session!")
                return
        }
	svc := ecs.New(session)

        rdb := config.RedisConnection
        for {
                time.Sleep(5*time.Minute) //TODO: how about increaed default pause for tasks cleaner to 10-15m? (to minimize tasks describe operations)

                keys, err := rdb.Keys(context.Background(), "*").Result()
                if err != nil {
                        log.WithError(err).Error("Failed to get list of keys!")
                        continue
                }
                log.WithField("keys", keys).Trace("cached session keys")

                // Construct pages of *string with 100 or fewer elements for requests. 100 is an AWS limitation for Describe* requests
                var taskIdsPtrs []*string
                for _, k := range keys {
                        taskId := k //local env required to generate new reference address to mew value
                        taskIdsPtrs = append(taskIdsPtrs, &taskId)
                }
                pages := paginate(taskIdsPtrs, 100)

                // Send DescribeTasks requests and track resources usage for STOPPED tasks
                for _, page := range pages {
                        describeTasksInput := ecs.DescribeTasksInput{
                                Cluster: &config.Conf.AwsCluster,
                                Tasks:   page,
                        }

			log.Trace("describeTasksInput: ", describeTasksInput)
                        output, err := svc.DescribeTasks(&describeTasksInput)
                        if err != nil {
                                log.WithError(err).Error("Failed to describe tasks!")
                        }

			var taskIds4Removal []string
			// Do not remove MISSING as any driver/browser sessionId we use can be removed from session map
/*			for _, failure := range output.Failures {
				//no sense to keep MISSING sessions as we can't detect resoutce usage anymore!
				// failures="[{\n  Arn: \"arn:aws:ecs:us-east-1:659932254483:task/9fc25c3e9c1c865e94b68061f020d083\",\n  Reason: \"MISSING\"\n}]"
				if *failure.Reason == "MISSING" {
					taskId := strings.Split(*failure.Arn, "/")[1]
					taskIds4Removal = append(taskIds4Removal, taskId)
				}
			}
*/

	                // analyze output.Tasks response for existing tasks in sessionmap
			for _, task := range output.Tasks {
				if *task.LastStatus == "STOPPED" {
					taskId := strings.Split(*task.TaskArn, "/")[2]

					session, err := sessionmap.Find(taskId, false)
					if err != nil {
						log.WithError(err).WithField("key", taskId).Error("Failed to get task session from sessionmap!")
						continue
					}

					log.WithField("taskId", taskId).Trace("StartedAt: ", *task.StartedAt)
					log.WithField("taskId", taskId).Trace("StoppedAt: ", *task.StoppedAt)
					startedAt := *task.StartedAt //local var needed to calculate difference via Sub(..)
					stoppedAt := *task.StoppedAt
					zebrunner.TrackResourcesUsage(session, stoppedAt.Sub(startedAt))

					taskIds4Removal = append(taskIds4Removal, taskId)
				}
			}

			// cleanup tracked task sessions
			for _, id := range taskIds4Removal {
				log.WithField("taskId", id).Trace("Removing task session")
				err = sessionmap.Remove(id)
				if err != nil {
					log.WithError(err).WithField("id", id).Error("Failed to remove task session from sessionmap!")
				}
			}

                }
        }
}

func ClearIdleSessions() {
	rdb := config.RedisConnection
	for {
		time.Sleep(config.Conf.IdleTimeout)
		keys, err := rdb.Keys(context.Background(), "*").Result()
		if err != nil {
			log.WithError(err).Error("Failed to get list of keys!")
			continue
		}

		for _, key := range keys {
			session, err := sessionmap.Find(key, false)
			if err != nil {
				log.WithError(err).WithField("key", key).Error("Failed to get session from sessionmap!")
				continue
			}

			if session.Status != sessionmap.SessionActive {
				continue
			}

                        log.WithField("session", session).Debug("analyzing session for idleTimeout")

			idleTimeout := float64(session.Capabilities.IdleTimeout)
			if idleTimeout == 0 {
				idleTimeout = config.Conf.IdleTimeout.Seconds()
			}

			idleTime := time.Since(session.AccessedAt).Seconds()
			if idleTime > idleTimeout {
				// Set stopped status and expiration time 10 minutes to be able to return "invalid session id" for requests
				session.Status = sessionmap.SessionStoppedIdle
				err = sessionmap.Write(key, session, 10*time.Minute)

				// [VD] do not execute CloseSession as it remove session from sessionmap and we can't return idle timeout errors to client
				//selenium.CloseSession(session)
				_, err = service.StopTask(session.TaskID)
				if err != nil {
					log.WithError(err).Error("Failed to stop idle driver task!")
				} else {
					log.WithField("_taskId", session.TaskID).WithField("workspace", session.Workspace).Warn("task aborted due to the idle timeout")
				}
			}
		}
	}
}

func ScaleCluster() {
	for {
		time.Sleep(10 * time.Second)
		service.ScaleUp()
	}
}

func ScaleDownCluster() {
	for {
		time.Sleep(30 * time.Second)
		service.ScaleDown()
	}
}

func RefreshTaskDefinition(image string) error {
	caps, err := capabilities.FromImage(image)
	if err != nil {
		log.WithError(err).WithField("image", image).Error("Failed to build capabilities for image!")
		return err
	}

	env, err := environment.Build("", caps, &config.Conf)
	if err != nil {
		log.WithError(err).WithField("image", image).Error("Failed to build execution environment!")
		return err
	}

	_, err = service.CreateTaskDefinition(env)
	if err != nil {
		log.WithError(err).WithField("image", image).Error("Failed to create task definition!")
		return err
	}

	return nil
}

func RefreshTaskDefinitions() {
	images, err := service.ListBrowsers()
	if err != nil {
		log.WithError(err).Error("Failed to get image list!")
	}

	for _, image := range images {
		time.Sleep(1000 * time.Millisecond)
		err = RefreshTaskDefinition(image)
		if err != nil {
			continue
		}
	}
}

func RefreshTaskDefinitionsFromFile(path string) {
	text, err := ioutil.ReadFile(path)
	if err != nil {
		log.WithError(err).Error("Failed to read file browsers.txt!")
	}
	lines := strings.Split(string(text), "\n")

	images := []string{}
	for _, line := range lines {
		if line != "" {
			images = append(images, line)
		}
	}

	log.WithField("images", images).Trace("refreshing task definition using file")
	for _, image := range images {
		time.Sleep(1000 * time.Millisecond)
		err = RefreshTaskDefinition(image)
		if err != nil {
			continue
		}
	}
}

func CleanZombieTasks() {
        //TODO: #427 move zombie tasks handler to new ClearTasks method
	session, err := awsSession.NewSession(&aws.Config{Region: &config.Conf.AwsRegion, MaxRetries: &config.Conf.AwsRetry})
	if err != nil {
		log.WithError(err).Error("Failed to create AWS session!")
		return
	}

	for {
		svc := ecs.New(session)
		tasks, err := service.GetClusterTasks(svc)
		if err != nil {
			log.WithError(err).Warn("Failed to get cluster tasks!")
		}

		for _, task := range tasks {
			//TODO: parametrize zombie timeout to be able to override via capabilities
			if time.Since(*task.CreatedAt) > 24*time.Hour {
				taskId := strings.Split(*task.TaskArn, "/")[2]
				service.StopTask(taskId)
			}
		}

		time.Sleep(1 * time.Hour)
	}
}

func paginate[T interface{}](l []T, size int) [][]T {
        numPages := int(math.Ceil(float64(len(l)) / float64(size)))
        pages := make([][]T, numPages)
        for i := 0; i < numPages; i++ {
                left := i * size
                right := (i + 1) * size
                if right > len(l) {
                        right = len(l)
                }
                pages[i] = l[left:right]
        }

        return pages
}


func main() {
	flag.Parse()

	log.SetLevel(config.Conf.ParseLogLevel())

	awsSess, err := service.InitAws()
	if err != nil {
		log.WithError(err).Fatal("Failed to init aws session")
	}
	service.AwsSess = awsSess

	rdb, err := config.InitCache()
	if err != nil {
		log.WithError(err).Fatal("Failed to init redis connection")
	}
	config.RedisConnection = rdb

	if config.Conf.BrowsersFile != "" {
		RefreshTaskDefinitionsFromFile(config.Conf.BrowsersFile)
	} else {
		RefreshTaskDefinitions()
	}
	log.Info("Task definitions refreshed finished")

	wg.Add(1)
	go ScaleCluster()
	if enableFastScaleDown {
		wg.Add(1)
		go ScaleDownCluster()
	}
	wg.Add(1)
	go ClearIdleSessions()

	wg.Add(1)
	go ClearTasks()

	wg.Add(1)
	go CleanZombieTasks()

	wg.Wait()
	log.Fatal("Background worker stopped!")
}
