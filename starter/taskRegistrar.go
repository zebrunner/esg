package starter

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
	"github.com/zebrunner/esg/service"
	"github.com/zebrunner/esg/utils"
)

type registerWaitRequest struct {
	NonEssentialErrCh chan error
	EssentialErrCh    chan error
	ResponseCh        chan string
}

func registerTask(ctx context.Context, env *environment.ExecutionEnvironment, routerUUID string, waitRequest registerWaitRequest) {
	svc := ecs.New(service.AwsSess)

	family, err := env.GetFamilyRevision()
	if err != nil {
		utils.SendToChanIfNotBlocked(waitRequest.EssentialErrCh, fmt.Errorf("image not found: '%s'", env.TaskDefinitionFamily))
		log.WithError(err).Error("image not found")

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
		CapacityProviderStrategy: []*ecs.CapacityProviderStrategyItem{{CapacityProvider: &env.CapacityProvider}},
	}
	l.WithField("runTaskInput", runTaskInput).Trace("Res runTaskInput")

	// TODO: explicitly minimize errors range to wait only by well-known reasons aka RESOURCE:CPU etc
	// [VD] "i" retry should be ~15 if instances can be started in 1 min and 25 if ~2 min
	var essentialError error
	var resourceAllocationEntity *resourcesToAllocate.ResourcesToAllocate
out:
	for i := 0; true; i++ {
		l := l.WithField("retry", i)
		var outputErr error = nil
		var sleepRateLimit time.Duration = 0

		select {
		case <-ctx.Done():
			essentialError = fmt.Errorf("timed out waiting for task register")
			break out
		default:
		}
		// do not pause after ctx deadline check and before ecs call
		resultRunTask, err := svc.RunTask(runTaskInput)
		if err != nil {
			l.WithError(err).Error("Task register failed.")
			outputErr = err
			sleepRateLimit = time.Duration(5+rand.Intn(10)) * time.Second

			// Not good solution but aws doesn't give a choice
			errStr := err.Error()
			if errStr == "ClientException: TaskDefinition not found." {
				essentialError = fmt.Errorf("image not found: '%s'", env.TaskDefinitionFamily)
				break out
			} else if errStr == "ClientException: Tasks provisioning capacity limit exceeded." || strings.Contains(errStr, "ThrottlingException: Rate exceeded") {
				sleepRateLimit = time.Duration(15+rand.Intn(15)) * time.Second
			}
		} else if len(resultRunTask.Failures) != 0 {
			outputErr = fmt.Errorf(*resultRunTask.Failures[0].Reason)
			l.WithError(outputErr).Debug("Task register failed. Response contains failures")
			sleepRateLimit = time.Duration(5+(rand.Intn(15))) * time.Second
		} else if len(resultRunTask.Tasks) == 0 {
			outputErr = fmt.Errorf("response doesn't contain tasks")
			l.WithError(outputErr).Debug("Task register failed")
			sleepRateLimit = time.Duration(5+(rand.Intn(5))) * time.Second
		}

		if outputErr == nil {
			// All is ok. We got task then we can return it.
			utils.SendToChanIfNotBlocked(waitRequest.ResponseCh, *resultRunTask.Tasks[0].TaskArn)

			if resourceAllocationEntity != nil {
				go func(rta *resourcesToAllocate.ResourcesToAllocate) {
					err := resourcesToAllocate.RemoveEntity(rta)
					if err != nil {
						log.WithError(err).Error("Failed to remove allocation resource from cache")
					}
				}(resourceAllocationEntity)
			}
			return
		}

		if resourceAllocationEntity == nil {
			go func() {
				resourceAllocationEntity = env.GetAllocationResources(routerUUID)
				err := resourcesToAllocate.AddEntity(resourceAllocationEntity)
				if err != nil {
					log.WithError(err).Error("Failed to add allocation resource to cache")
				}
			}()
		}

		time.Sleep(sleepRateLimit)
	}

	utils.SendToChanIfNotBlocked(waitRequest.EssentialErrCh, essentialError)

	if resourceAllocationEntity != nil {
		go func(rta *resourcesToAllocate.ResourcesToAllocate) {
			err := resourcesToAllocate.RemoveEntity(rta)
			if err != nil {
				log.WithError(err).Error("Failed to remove allocation resource from cache")
			}
		}(resourceAllocationEntity)
	}
}

func WaitForTaskRegister(ctx context.Context, env *environment.ExecutionEnvironment, routerUUID string) *registerWaitRequest {
	waitReq := registerWaitRequest{
		NonEssentialErrCh: make(chan error),
		EssentialErrCh:    make(chan error),
		ResponseCh:        make(chan string),
	}

	go registerTask(ctx, env, routerUUID, waitReq)

	return &waitReq
}
