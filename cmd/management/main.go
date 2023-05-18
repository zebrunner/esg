package main

import (
	"context"
	"flag"
	"math"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/ecs"
	"github.com/zebrunner/esg/capabilities"
	"github.com/zebrunner/esg/environment"
	"github.com/zebrunner/esg/service"
	sessionmap "github.com/zebrunner/esg/sessinonmap"
	"github.com/zebrunner/esg/utils"

	awsSession "github.com/aws/aws-sdk-go/aws/session"
	log "github.com/sirupsen/logrus"
	"github.com/zebrunner/esg/config"

	"github.com/zebrunner/esg/zebrunner"
)

var (
	wg                  sync.WaitGroup
)

func ClearTasks() {

        session, err := awsSession.NewSession(&aws.Config{Region: &config.Conf.AwsRegion, MaxRetries: &config.Conf.AwsRetry})
        if err != nil {
                log.WithError(err).Error("Failed to create AWS session!")
                return
        }
	svc := ecs.New(session)

        rdb := config.RedisConnection
        for {
                time.Sleep(1*time.Minute)

                keys, err := rdb.Keys(context.Background(), "*").Result()
                if err != nil {
                        log.WithError(err).Error("Failed to get list of keys!")
                        continue
                }
		if len(keys) > 0 {
			log.WithField("keys", keys).Trace("cached session keys")
		}

                for _, key := range keys {
                        session, err := sessionmap.Find(key, false)

						if session==nil {
							log.WithField("session key", key).Error("Not found")
							//TDDO: since this session map entry does not exist on aws, we should remove it from sessionMap.
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
				if *task.LastStatus == "STOPPED" || *task.LastStatus == "RUNNING" {
					taskId := strings.Split(*task.TaskArn, "/")[2]
					l := log.WithFields(log.Fields{"_taskId": taskId})

					session, err := sessionmap.Find(taskId, false)
					if err != nil {
						log.WithField("taskId", taskId).WithError(err).Error("Failed to get task session from sessionmap!")
						continue
					}

					l = log.WithFields(log.Fields{"_taskId": taskId, "workspace": session.Workspace})

					if *task.LastStatus == "STOPPED" {
						if task.StartedAt != nil && task.StoppedAt != nil {
							// don't calculate timing for terminated tasks by AWS due to the missted StartedAt!
							//	StopCode: \"TerminationNotice\"
							//	StoppedReason: \"Host EC2 (instance i-03dba81187d65ce7e) terminated.\"
							l.Trace("StartedAt: ", *task.StartedAt)
							l.Trace("StoppedAt: ", *task.StoppedAt)
							startedAt := *task.StartedAt //local var needed to calculate difference via Sub(..)
							stoppedAt := *task.StoppedAt
							zebrunner.TrackResourcesUsage(session, stoppedAt.Sub(startedAt))
						}

						if !strings.HasPrefix(session.Capabilities.Image, "public.ecr.aws/zebrunner/cypress-") {
							// #503: суpress tests aborted automatically
							// automatic abort of the public.ecr.aws/zebrunner/cypress-* should be prohibited as execution is control by parent cyserver process
							zebrunner.AbortTask(session, task)
						}
						taskIds4Removal = append(taskIds4Removal, taskId)
					}

					if *task.LastStatus == "RUNNING" {
						maxTimeout := config.Conf.MaxTimeout
						if (session.Capabilities.MaxTimeout != 0) {
							maxTimeout = time.Duration(session.Capabilities.MaxTimeout) * time.Second
						}
						l.Debug("maxTimeout: ", maxTimeout)
						if task.CreatedAt != nil && time.Since(*task.CreatedAt) > maxTimeout {
							// stop zombie task
							service.StopTask(taskId)
							l.WithField("maxTimeout", maxTimeout).Warn("task aborted due to the max timeout")
							// do not register resource usage and don't mark taskId for removal!
						}
					}
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

	env, err := environment.Build("", caps)
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
	images := getImageList()

	for _, image := range images {
		time.Sleep(1000 * time.Millisecond)
		err := RefreshTaskDefinition(image)
		if err != nil {
			continue
		}
	}
}

func getImageList() []string  {
	images, err := utils.ListBrowsers()
	if err != nil {
		log.WithError(err).Error("Failed to get image list!")
		os.Exit(1)
	}

	return images
}

func getImageSet() map[string]bool {
	images := getImageList()

	imagesSet := make(map[string]bool, cap(images))
	for _, image := range images {
		imagesSet[image]= true
	}

	return imagesSet
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

func AddTaskDefinitions() {
	log.Debug("Saved list of images for task defenition refresh: ")
	imagesSet := getImageSet()

	for {
		time.Sleep(24 * time.Hour)

		updatedImages := getImageList()
		for _, image := range updatedImages {
			if present := imagesSet[image]; !present {
				log.Info("Found new image in ecr: " + image)
				err := RefreshTaskDefinition(image)
				if err == nil {
					imagesSet[image] = true
				}
			}
		}

	}
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

	RefreshTaskDefinitions()
	log.Info("Task definitions updates finished")

	wg.Add(1)
	go ScaleCluster()

	wg.Add(1)
	go ScaleDownCluster()

	wg.Add(1)
	go ClearTasks()
	
	wg.Add(1)
	go AddTaskDefinitions()	

	wg.Wait()
	log.Fatal("Background worker stopped!")
}
