package main

import (
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

func ClearTasks() {
	session, err := awsSession.NewSession(&aws.Config{Region: &config.Conf.AwsRegion, MaxRetries: &config.Conf.AwsRetry})
	if err != nil {
		log.WithError(err).Error("Failed to create AWS session!")
		return
	}

	svc := ecs.New(session)
	var wg sync.WaitGroup

	for {
		time.Sleep(1 * time.Minute)

		keys, err := sessionmap.Keys()
		if err != nil {
			log.WithError(err).Error("Failed to get list of keys!")
			continue
		}

		if len(keys) > 0 {
			log.WithField("keys", keys).Trace("cached session keys")
		}

		wg.Add(1)
		go StopIdleTasks(keys, &wg)

		wg.Add(1)
		go CheckTasksStatus(keys, svc, &wg)

		wg.Wait()
	}
}

func StopIdleTasks(keys []string, wg *sync.WaitGroup) {
	for _, key := range keys {
		session, _ := sessionmap.Find(key, false)

		if session == nil {
			log.WithField("session key", key).Error("Not found")
			continue
		}

		if session.Status != sessionmap.SessionActive {
			continue
		}

		log.WithField("session", session).Debug("StopIdleTasks: analyzing session for idleTimeout")

		idleTimeout := float64(session.Capabilities.IdleTimeout)
		if idleTimeout == 0 {
			idleTimeout = config.Conf.IdleTimeout.Seconds()
		}

		idleTime := time.Since(session.AccessedAt).Seconds()
		if idleTime > idleTimeout {
			// [VD] do not execute CloseSession as it remove session from sessionmap and we can't return idle timeout errors to client
			//selenium.CloseSession(session)
			_, err := service.StopTask(session.TaskID)
			if err != nil {
				log.WithError(err).Error("Failed to stop idle driver task!")
			} else {
				// Set stopped status and expiration time 10 minutes to be able to return "invalid session id" for requests
				session.Status = sessionmap.SessionStoppedIdle
				sessionmap.Write(key, session, 10*time.Minute)
				log.WithField("_taskId", session.TaskID).WithField("workspace", session.Workspace).Warn("task aborted due to the idle timeout")
			}
		}
	}

	wg.Done()
}

func CheckTasksStatus(keys []string, svc *ecs.ECS, wg *sync.WaitGroup) {
	var taskIdsPtrs []*string = aws.StringSlice(keys)

	// Construct pages of *string with 100 or fewer elements for requests. 100 is an AWS limitation for Describe* requests
	pages := paginate(taskIdsPtrs, 100)

	tasks := make([]*ecs.Task, 0)
	// Send DescribeTasks requests and save response tasks into array
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

		tasks = append(tasks, output.Tasks...)
	}
	// track STOPPED tasks id for removing them from sessionMap
	var taskIds4Removal []string

	/* Do not remove MISSING as any driver/browser sessionId we use can be removed from session map
	for _, failure := range tasks {
		//no sense to keep MISSING sessions as we can't detect resoutce usage anymore!
		// failures="[{\n  Arn: \"arn:aws:ecs:us-east-1:659932254483:task/9fc25c3e9c1c865e94b68061f020d083\",\n  Reason: \"MISSING\"\n}]"
		if *failure.Reason == "MISSING" {
			taskId := strings.Split(*failure.Arn, "/")[1]
			taskIds4Removal = append(taskIds4Removal, taskId)
		}
	}*/

	// analyze tasks response
	for _, task := range tasks {
		taskId := strings.Split(*task.TaskArn, "/")[2]
		l := log.WithFields(log.Fields{"_taskId": taskId})

		session, err := sessionmap.Find(taskId, false)
		if err != nil {
			l.WithError(err).Error("Failed to get task session from sessionmap!")
			// if task is in a Running stage and still not registered in sessionMap for SessionStartupTimeout time
			// stop this task
			if *task.LastStatus == "RUNNING" && *task.DesiredStatus != "STOPPED" {
				sessStartup := config.Conf.SessionStartupTimeout.Seconds()
				if task.CreatedAt != nil && time.Since(*task.CreatedAt).Seconds() > sessStartup {
					l.WithField("sessionStartupTimeout", sessStartup).Warn("Task is running and wasn't registered in sessionMap in time. Aborting")
					_, err := service.StopTask(taskId)
					if err != nil {
						l.WithError(err).Error("Failed to stop the task")
					}
				}
			}
			continue
		}

		// add task id for removal and track resources usage for STOPPED tasks
		if *task.LastStatus == "STOPPED" {
			l = l.WithFields(log.Fields{"workspace": session.Workspace})

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

		// stop zombie and UNHEALTHY tasks that are not pending for stop.
		// resource usage register and taskId mark for removal is performed only for stopped tasks
		if *task.LastStatus == "RUNNING" && *task.DesiredStatus != "STOPPED" {
			if *task.HealthStatus == "UNHEALTHY" {
				l.Warn("Aborting task due to UNHEALTHY HealthStatus")
				_, err := service.StopTask(taskId)
				if err != nil {
					l.WithError(err).Error("Failed to stop the task")
				}
			} else {
				maxTimeout := config.Conf.MaxTimeout
				if session.Capabilities.MaxTimeout != 0 {
					maxTimeout = time.Duration(session.Capabilities.MaxTimeout) * time.Second
				}
				l.Debug("maxTimeout: ", maxTimeout)

				if task.CreatedAt != nil && time.Since(*task.CreatedAt) > maxTimeout {
					l.WithField("maxTimeout", maxTimeout).Warn("Aborting task due to the max timeout")
					_, err := service.StopTask(taskId)
					if err != nil {
						l.WithError(err).Error("Failed to stop the task")
					}
				}
			}
		}
	}

	// cleanup tracked task sessions
	for _, id := range taskIds4Removal {
		log.WithField("taskId", id).Trace("Removing task session")
		err := sessionmap.Remove(id)
		if err != nil {
			log.WithError(err).WithField("id", id).Error("Failed to remove task session from sessionmap!")
		}
	}
	wg.Done()
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

func getImageList() []string {
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
		imagesSet[image] = true
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

	var wg sync.WaitGroup
	wg.Add(1)

	go ScaleCluster()

	go ScaleDownCluster()

	go ClearTasks()

	go AddTaskDefinitions()

	wg.Wait()
	log.Fatal("Background worker stopped!")
}
