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

		var resultRunTask *ecs.RunTaskOutput
		resultRunTask, err := svc.RunTask(runTaskInput)
		if err != nil {
			l.WithError(err).Debug("Task register failed.")
			// Not good solution but aws doesn't give a choice
			errStr := err.Error()
			if errStr == "ClientException: TaskDefinition not found." {
				waitRequest.EssentialErrCh <- fmt.Errorf("image not found: '%s'", env.TaskDefinitionFamily)
				return
			} else if errStr == "ClientException: Tasks provisioning capacity limit exceeded." {
				// wait for 15 seconds (repeated until new instances will be provided and provisioning tasks will get to the next phase)
				time.Sleep(time.Duration(15 * time.Second))
			} else if strings.Contains(errStr, "ThrottlingException: Rate exceeded") {
				// increase average wait time based on retry count
				// min -> 1 sec on first retry
				// max -> 125 sec on last retry
				time.Sleep(time.Duration((i+1)*(1+rand.Intn(5))) * time.Second)
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
