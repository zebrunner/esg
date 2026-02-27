package starter

import (
	"context"
	"fmt"
	"math/rand"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ecs"
	ecsTypes "github.com/aws/aws-sdk-go-v2/service/ecs/types"
	"github.com/google/uuid"
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

// getPlacementStrategy returns the placement strategy based on retry attempt
// Attempts 0-1: binpack by memory
// Attempts 2-3: binpack by cpu
// Attempts 4+: no placement strategy (let ECS decide)
func getPlacementStrategy(attempt int) ([]ecsTypes.PlacementStrategy, string) {
	switch {
	case attempt < 2:
		return []ecsTypes.PlacementStrategy{
			{
				Field: aws.String("memory"),
				Type:  ecsTypes.PlacementStrategyTypeBinpack,
			},
		}, "binpack:memory"
	case attempt < 4:
		return []ecsTypes.PlacementStrategy{
			{
				Field: aws.String("cpu"),
				Type:  ecsTypes.PlacementStrategyTypeBinpack,
			},
		}, "binpack:cpu"
	default:
		return nil, "none"
	}
}

func registerTask(ctx context.Context, env *environment.ExecutionEnvironment, routerUUID string, waitRequest registerWaitRequest) {
	svc := ecs.NewFromConfig(service.AwsCfg)

	family, err := env.GetFamilyRevision()
	if err != nil {
		utils.SendToChanIfNotBlocked(waitRequest.EssentialErrCh, fmt.Errorf("image not found: '%s'", env.TaskDefinitionFamily))
		log.WithError(err).Error("image not found")

		return
	}
	l := log.WithField("family", env.TaskDefinitionFamily)

	// TODO: explicitly minimize errors range to wait only by well-known reasons aka RESOURCE:CPU etc
	// [VD] "i" retry should be ~15 if instances can be started in 1 min and 25 if ~2 min
	var essentialError error
	var resourceAllocationEntity *resourcesToAllocate.ResourcesToAllocate
	var lastPlacementStrategy string
out:
	for i := 0; true; i++ {
		attemptID := uuid.New().String()[:8]
		placementStrategy, strategyName := getPlacementStrategy(i)

		// Log only when placement strategy changes (skip first attempt)
		if i > 0 && strategyName != lastPlacementStrategy {
			l.WithFields(log.Fields{"attempt": i, "strategy": strategyName}).Debug("Placement strategy changed")
		}
		lastPlacementStrategy = strategyName

		l := l.WithFields(log.Fields{"retry": i, "attemptID": attemptID})

		runTaskInput := &ecs.RunTaskInput{
			Cluster:                  aws.String(config.Conf.AwsCluster),
			TaskDefinition:           aws.String(family),
			Overrides:                &ecsTypes.TaskOverride{ContainerOverrides: env.ContainerOverrides()},
			PlacementStrategy:        placementStrategy,
			CapacityProviderStrategy: []ecsTypes.CapacityProviderStrategyItem{{CapacityProvider: aws.String(env.CapacityProvider)}},
			Tags:                     service.BuildRunTaskTags(),
		}

		var outputErr error = nil
		var sleepRateLimit time.Duration = 0

		select {
		case <-ctx.Done():
			essentialError = fmt.Errorf("timed out waiting for task register")
			break out
		default:
		}

		resultRunTask, err := svc.RunTask(ctx, runTaskInput)
		if err != nil {
			l.WithError(err).Error("Task register failed")
			outputErr = err
			sleepRateLimit = time.Duration(5+rand.Intn(10)) * time.Second

			errStr := err.Error()
			if errStr == "ClientException: TaskDefinition not found." {
				essentialError = fmt.Errorf("image not found: '%s'", env.TaskDefinitionFamily)
				break out
			} else if errStr == "ClientException: Tasks provisioning capacity limit exceeded." || strings.Contains(errStr, "ThrottlingException: Rate exceeded") {
				sleepRateLimit = time.Duration(15+rand.Intn(15)) * time.Second
			}
		} else if len(resultRunTask.Failures) != 0 {
			outputErr = fmt.Errorf("%s", aws.ToString(resultRunTask.Failures[0].Reason))
			l.WithError(outputErr).Debug("Task register failed: response contains failures")
			sleepRateLimit = time.Duration(5+(rand.Intn(15))) * time.Second
		} else if len(resultRunTask.Tasks) == 0 {
			outputErr = fmt.Errorf("response doesn't contain tasks")
			l.WithError(outputErr).Debug("Task register failed: no tasks in response")
			sleepRateLimit = time.Duration(5+(rand.Intn(5))) * time.Second
		}

		if outputErr == nil {
			l.Debugf("task register attempt successful, placement strategy: %s, result task arn: %s", strategyName, aws.ToString(resultRunTask.Tasks[0].TaskArn))
			utils.SendToChanIfNotBlocked(waitRequest.ResponseCh, aws.ToString(resultRunTask.Tasks[0].TaskArn))

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
