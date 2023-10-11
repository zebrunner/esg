package service

import (
	"context"
	"fmt"
	"math/rand"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/ecs"
	log "github.com/sirupsen/logrus"
	"github.com/zebrunner/esg/cachemaps/resourcesToAllocate"
	"github.com/zebrunner/esg/config"
	"github.com/zebrunner/esg/environment"
)

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
	markedToAllocate := false
	for i := 0; true; i++ {
		l := l.WithField("retry", i)

		select {
		case <-ctx.Done():
			if markedToAllocate {
				resourcesToAllocate.RemoveEntity(env.RouterUUID)
			}
			return
		default:
		}

		// do not pause after ctx deadline check and before ecs call

		var resultRunTask *ecs.RunTaskOutput
		resultRunTask, err := svc.RunTask(runTaskInput)
		if err != nil {
			// Not good solution but aws doesn't give a choice
			errStr := err.Error()
			if errStr == "ClientException: TaskDefinition not found." {
				l.WithError(err).Error("Task register failed.")
				if markedToAllocate {
					resourcesToAllocate.RemoveEntity(env.RouterUUID)
				}
				waitRequest.EssentialErrCh <- fmt.Errorf("image not found: '%s'", env.TaskDefinitionFamily)
				return
			}

			if errStr == "ClientException: Tasks provisioning capacity limit exceeded." || strings.Contains(errStr, "ThrottlingException: Rate exceeded") {
				l.WithError(err).Trace("Task register failed.")
				if !markedToAllocate {
					resourcesToAllocate.AddEntity(env.CalculateResources())
					if err != nil {
						l.Info("Failed to cache resource allocation")
					}
					markedToAllocate = true
				}
				sleepRateLimit := time.Duration(20+rand.Intn(4)) * time.Second
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

		if markedToAllocate {
			resourcesToAllocate.RemoveEntity(env.RouterUUID)
		}

		// All is ok. We got task then we can return it.
		waitRequest.ResponseCh <- *resultRunTask.Tasks[0].TaskArn
		return
	}

	waitRequest.NonEssentialErrCh <- outputErr
}

func WaitForTaskRegister(ctx context.Context, env environment.ExecutionEnvironment) *registerWaitRequest {
	waitReq := registerWaitRequest{
		NonEssentialErrCh: make(chan error, 1),
		EssentialErrCh:    make(chan error, 1),
		ResponseCh:        make(chan string, 1),
	}

	go registerTask(ctx, env, waitReq)

	return &waitReq
}
