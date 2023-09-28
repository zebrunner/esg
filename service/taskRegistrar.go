package service

import (
	"context"
	"fmt"
	"math/rand"
	"strings"
	"sync/atomic"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/ecs"
	log "github.com/sirupsen/logrus"
	"github.com/zebrunner/esg/config"
	"github.com/zebrunner/esg/environment"
)

// miliseconds
var (
	registerPause  int64 = 0
	pauseIncrement int64 = 100
)

func getPause() int64 {
	pause := registerPause
	atomic.AddInt64(&registerPause, pauseIncrement)

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Duration(pause+pauseIncrement)*time.Millisecond)
		defer cancel()
		<-ctx.Done()
		atomic.AddInt64(&registerPause, -1*pauseIncrement)
	}()

	return pause
}

type registerWaitRequest struct {
	NonEssentialErrCh chan error
	EssentialErrCh    chan error
	ResponseCh        chan string
}

func registerTask(ctx context.Context, env environment.ExecutionEnvironment, waitRequest registerWaitRequest) {
	svc := ecs.New(AwsSess)

	family, err := env.GetFamilyRevision()
	if err != nil {
		waitRequest.EssentialErrCh <- fmt.Errorf("image not found: '%s'", env.TaskDefinitionFamily)
		return
	}
	l := log.WithField("family", env.TaskDefinitionFamily)

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
	l.WithField("runTaskInput", runTaskInput).Trace("Res runTaskInput")

	// TODO: explicitly minimize errors range to wait only by well-known reasons aka RESOURCE:CPU etc
	// TODO: convert existing hard-coded 25 retries into the queue or provisioning timeout: https://github.com/zebrunner/esg/issues/72
	// [VD] "i" retry should be ~15 if instances can be started in 1 min and 25 if ~2 min
	var outputErr error
	for i := 0; i < 25; i++ {

		l := l.WithField("retry", i)

		select {
		case <-ctx.Done():
			return
		default:
		}
		// Random sleep to fix problems with parallel 100+ threads startup. Not applicable for generic tasks!
		//TODO: uncomment before release!
		/*		if env.TaskDefinitionFamily != "generic" {
					sleep := time.Duration(rand.Intn(30)) * time.Second
					time.Sleep(sleep)
				}
		*/

		pause := getPause()
		time.Sleep(time.Duration(pause) * time.Millisecond)

		var resultRunTask *ecs.RunTaskOutput
		resultRunTask, err := svc.RunTask(runTaskInput)
		if err != nil {
			l.WithError(err).Error("Task register failed.")
			// Not good solution but aws doesn't give a choice
			errStr := err.Error()
			if errStr == "ClientException: TaskDefinition not found." {
				waitRequest.EssentialErrCh <- fmt.Errorf("image not found: '%s'", env.TaskDefinitionFamily)
				return
			}

			if errStr == "ClientException: Tasks provisioning capacity limit exceeded." || strings.Contains(errStr, "ThrottlingException: Rate exceeded") {
				sleepRateLimit := time.Duration(15 + rand.Intn(15))
				time.Sleep(sleepRateLimit)
			}

			outputErr = err
			continue
		}

		if len(resultRunTask.Failures) != 0 {
			outputErr = fmt.Errorf(*resultRunTask.Failures[0].Reason)
			l.WithError(outputErr).Debug("Task register failed. Response contains failures")
			continue
		}

		if len(resultRunTask.Tasks) == 0 {
			outputErr = fmt.Errorf("response doesn't contain tasks")
			l.WithError(outputErr).Debug("Task register failed")
			continue
		}

		// All is ok. We got task then we can return it.
		waitRequest.ResponseCh <- *resultRunTask.Tasks[0].TaskArn
		return
	}

	waitRequest.NonEssentialErrCh <- outputErr
}

func WaitForTaskRegister(ctx context.Context, env environment.ExecutionEnvironment) *registerWaitRequest {
	waitReq := registerWaitRequest{
		NonEssentialErrCh: make(chan error),
		EssentialErrCh:    make(chan error),
		ResponseCh:        make(chan string),
	}

	go registerTask(ctx, env, waitReq)

	return &waitReq
}
