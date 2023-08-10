package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"path"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go/service/ecs"
	"github.com/gin-gonic/gin"
	log "github.com/sirupsen/logrus"
	"github.com/zebrunner/esg/cachemaps/sessionmap"
	"github.com/zebrunner/esg/cachemaps/taskmap"
	"github.com/zebrunner/esg/capabilities"
	"github.com/zebrunner/esg/config"
	"github.com/zebrunner/esg/environment"
	"github.com/zebrunner/esg/selenium"
	"github.com/zebrunner/esg/utils"
)

type ServiceStarter interface {
	StartService() (map[string]interface{}, *utils.SeleniumError)
	// append(p phase) []phase
}

// essential error -> stop service, non essential error -> retry service start, response chan -> successfull phase execution
type waitAdapter[Input, Output any] func(context.Context, Input) (chan<- error, chan<- error, chan<- Output)

var (
	registerTaskAdapter waitAdapter[environment.ExecutionEnvironment, string] = func(ctx context.Context, env environment.ExecutionEnvironment) (chan<- error, chan<- error, chan<- string) {
		waitRequest := WaitForTaskRegister(ctx, env)
		return waitRequest.essentialErrCh, waitRequest.nonEssentialErrCh, waitRequest.taskArnCh
	}

	startTaskAdapter waitAdapter[string, *ecs.Task] = func(ctx context.Context, taskArn string) (chan<- error, chan<- error, chan<- *ecs.Task) {
		waitRequest := taskWaiter.waitFor(ctx, taskArn)
		return waitRequest.essentialErrCh, waitRequest.nonEssentialErrCh, waitRequest.responseChan
	}
)

type phase[InputType any] func(context.Context, chan<- error, chan<- error, chan<- InputType)

type phaseChainer[InputType any, OutputType any] struct {
	waitForPhase func(context.Context, phase[InputType]) OutputType
	nextPhase    phase[OutputType]
}

type phaseManager struct {
	phaseChainers []phaseChainer[any, any]
}

type startBasis struct {
	Env          *environment.ExecutionEnvironment
	GinCtx       *gin.Context
	ReqCaps      *capabilities.RequestCaps
	Log          *log.Entry
	PhaseManager *phaseManager
	ServiceStart time.Time
}

func (starter startBasis) StartService() (map[string]interface{}, *utils.SeleniumError) {
	starter.ServiceStart = time.Now()
	ctx, ctxCancel := context.WithTimeout(context.Background(), config.Conf.ServiceStartupTimeout)
	defer ctxCancel()

	return nil, nil
}

func (starter startBasis) waitForTaskRegister(ctx context.Context, p phase[string]) *ecs.Task {
	var taskId string
	essentialErrCh, nonEsentialErrCh, taskArnCh := make(chan error, 0), make(chan error, 0), make(chan string, 0)
	p(ctx, essentialErrCh, nonEsentialErrCh, taskArnCh)
	select {
	case <-ctx.Done():
		l.WithField("latency", time.Since(serviceStartupTime)).Info("service startup timed out")
		return nil, utils.CreationErr(fmt.Errorf("service startup timed out"))
	case err := <-essentialErrCh:
		l.WithField("latency", time.Since(serviceStartupTime)).WithError(err).Info("Got esential error, stopping service starter")
		return nil, utils.CreationErr(err)
	case err := <-nonEsentialErrCh:
		l.WithField("latency", time.Since(serviceStartupTime)).WithError(err).Warn("Got non esential error")
		continue
	case taskArn := <-taskArnCh:
		taskId = strings.Split(taskArn, "/")[2]
		l = l.WithField("_taskId", taskId)

		cachedTask := &taskmap.Task{
			ID:           taskId,
			Capabilities: starter.Env.Capabilities,
			Status:       taskmap.TaskGeneric, // TODO: change status to active when CloseSession() for generic tasks will be called
			Workspace:    starter.Env.Workspace,
			HealthAt:     time.Now(), //TODO: remove HealthAt as only healthcheck integrated into the generic as well
			Network:      *starter.Env.Network,
		}

		err := taskmap.Write(cachedTask.ID, cachedTask, 0)
		if err != nil {
			l.WithField("latency", time.Since(serviceStartupTime)).WithError(fmt.Errorf("failed to cache task: %v", err)).Warn()
			err := StopTask(cachedTask.ID, taskmap.TaskStartupFailure)
			if err != nil {
				l.WithField("latency", time.Since(serviceStartupTime)).WithError(err).Warn("Failed to stop task")
			}
			continue
		}

		// return response for generic task as soon as possible.
		resp := make(map[string]interface{}, 0)
		resp["taskId"] = cachedTask.ID

		l.WithField("resp", resp).Debug("Response")
		l.WithField("latency", time.Since(serviceStartupTime)).Info("service started")
		return resp, nil
	}
}

// type phase [T interface{}]func(context.Context, chan<- error, chan<- error, chan<- T)

// registerTask()
// startTask()
// setNetwork()
// startDriver()

// func (starter startBasis) append(p phase) []phase {
// 	starter.PhaseChain = append(starter.PhaseChain, p)
// 	return starter.PhaseChain
// }

func (starter startBasis) setHostPort(task *ecs.Task) error {
	for _, endpoint := range starter.Env.Network.Endpoints {
		hostPort, ok := searchHostPort(task, endpoint.ContainerPort)
		if !ok {
			return fmt.Errorf("host port not found. containerPort=%d", endpoint.ContainerPort)
		}
		endpoint.HostPort = hostPort
	}

	return nil
}

type genericStarter startBasis

func (starter genericStarter) StartService() (map[string]interface{}, *utils.SeleniumError) {
	serviceStartupTime := time.Now()
	ctx, ctxCancel := context.WithTimeout(context.Background(), config.Conf.ServiceStartupTimeout)
	defer ctxCancel()

	starter.Log.Info("service starting")
	for i := 0; true; i++ {
		l := starter.Log.WithField("attempt", i)

		var taskId string
		essentialErrCh, nonEsentialErrCh, taskArnCh := waitForPhase(ctx, startBasis(starter).registerTask)
		select {
		case <-ctx.Done():
			l.WithField("latency", time.Since(serviceStartupTime)).Info("service startup timed out")
			return nil, utils.CreationErr(fmt.Errorf("service startup timed out"))
		case err := <-essentialErrCh:
			l.WithField("latency", time.Since(serviceStartupTime)).WithError(err).Info("Got esential error, stopping service starter")
			return nil, utils.CreationErr(err)
		case err := <-nonEsentialErrCh:
			l.WithField("latency", time.Since(serviceStartupTime)).WithError(err).Warn("Got non esential error")
			continue
		case taskArn := <-taskArnCh:
			taskId = strings.Split(taskArn, "/")[2]
			l = l.WithField("_taskId", taskId)

			cachedTask := &taskmap.Task{
				ID:           taskId,
				Capabilities: starter.Env.Capabilities,
				Status:       taskmap.TaskGeneric, // TODO: change status to active when CloseSession() for generic tasks will be called
				Workspace:    starter.Env.Workspace,
				HealthAt:     time.Now(), //TODO: remove HealthAt as only healthcheck integrated into the generic as well
				Network:      *starter.Env.Network,
			}

			err := taskmap.Write(cachedTask.ID, cachedTask, 0)
			if err != nil {
				l.WithField("latency", time.Since(serviceStartupTime)).WithError(fmt.Errorf("failed to cache task: %v", err)).Warn()
				err := StopTask(cachedTask.ID, taskmap.TaskStartupFailure)
				if err != nil {
					l.WithField("latency", time.Since(serviceStartupTime)).WithError(err).Warn("Failed to stop task")
				}
				continue
			}

			// return response for generic task as soon as possible.
			resp := make(map[string]interface{}, 0)
			resp["taskId"] = cachedTask.ID

			l.WithField("resp", resp).Debug("Response")
			l.WithField("latency", time.Since(serviceStartupTime)).Info("service started")
			return resp, nil
		}
	}

	return nil, utils.CreationErr(fmt.Errorf("failed to start service"))
}

type cypressStarter startBasis

func (starter cypressStarter) StartService() (map[string]interface{}, *utils.SeleniumError) {
	serviceStartupTime := time.Now()
	ctx, ctxCancel := context.WithTimeout(context.Background(), config.Conf.ServiceStartupTimeout)
	defer ctxCancel()

	starter.Log.Info("service starting")
	for i := 0; true; i++ {
		l := starter.Log.WithField("attempt", i)

		var taskArn string
		essentialErrCh, nonEsentialErrCh, taskArnCh := waitForPhase(ctx, startBasis(starter).registerTask)
		select {
		case <-ctx.Done():
			l.WithField("latency", time.Since(serviceStartupTime)).Info("service startup timed out")
			return nil, utils.CreationErr(fmt.Errorf("service startup timed out"))
		case err := <-essentialErrCh:
			l.WithField("latency", time.Since(serviceStartupTime)).WithError(err).Info("Got esential error, stopping service starter")
			return nil, utils.CreationErr(err)
		case err := <-nonEsentialErrCh:
			l.WithField("latency", time.Since(serviceStartupTime)).WithError(err).Warn("Got non esential error")
			continue
		case taskArn = <-taskArnCh:
			//got task arn
		}

		taskId := strings.Split(taskArn, "/")[2]
		l = l.WithField("_taskId", taskId)

		cachedTask, err := taskmap.CreateEntity(taskId, starter.Env)
		if err != nil {
			l.WithError(fmt.Errorf("failed to cache task: %v", err)).Warn()
			err := StopTask(cachedTask.ID, taskmap.TaskStartupFailure)
			if err != nil {
				l.WithError(err).Warn("Failed to stop task")
			}
			continue
		}

		l.Info("task starting")
		var task *ecs.Task
		taskReq := taskWaiter.waitFor(ctx, taskArn)
		select {
		case <-taskReq.ctx.Done():
			l.WithField("latency", time.Since(serviceStartupTime)).Info("service startup timed out")
			err = StopTask(cachedTask.ID, taskmap.TaskStartupFailure)
			if err != nil {
				l.WithError(err).Warn("Failed to stop task")
			}
			return nil, utils.CreationErr(fmt.Errorf("service startup timed out"))
		case err := <-taskReq.nonEssentialErrCh:
			l.WithField("latency", time.Since(serviceStartupTime)).WithError(fmt.Errorf("failed to run task: %v", err)).Warn()
			err = StopTask(cachedTask.ID, taskmap.TaskStartupFailure)
			if err != nil {
				l.WithError(err).Warn("Failed to stop task")
			}
			continue
		case task = <-taskReq.responseChan:
			// timediff between HealthAt (current time) and task.startedAt should be cut during resources tracking to bill only actual (net) time
			cachedTask.HealthAt = time.Now()
			// consider cyserver as generic task
			cachedTask.Status = taskmap.TaskGeneric
			err = taskmap.Write(cachedTask.ID, cachedTask, 0)
			if err != nil {
				l.WithError(fmt.Errorf("failed to recache task: %v", err)).Warn()
				err := StopTask(cachedTask.ID, taskmap.TaskStartupFailure)
				if err != nil {
					l.WithError(err).Warn("Failed to stop task")
				}
				continue
			}
			l.WithField("latency", time.Since(serviceStartupTime)).Info("task started")
		}

		l.Debug("setting network environment")
		instanceReq := instanceWorker.waitForInstance(ctx, task)
		select {
		case <-instanceReq.ctx.Done():
			l.WithField("latency", time.Since(serviceStartupTime)).Info("service startup timed out")
			err = StopTask(cachedTask.ID, taskmap.TaskStartupFailure)
			if err != nil {
				l.WithError(err).Warn("Failed to stop task")
			}
			return nil, utils.CreationErr(fmt.Errorf("service startup timed out"))
		case err := <-instanceReq.errorChan:
			l.WithField("latency", time.Since(serviceStartupTime)).WithError(err).Warn("Failed to get instance")
			err = StopTask(cachedTask.ID, taskmap.TaskStartupFailure)
			if err != nil {
				l.WithError(err).Warn("Failed to stop task")
			}
			continue
		case instance := <-instanceReq.responseChan:
			if config.Conf.UsePublicIp {
				starter.Env.Network.IP = *instance.PublicIpAddress
			} else {
				starter.Env.Network.IP = *instance.PrivateIpAddress
			}

			err = startBasis(starter).setHostPort(task)
			if err != nil {
				l.WithField("latency", time.Since(serviceStartupTime)).WithError(err).Warn("failed to set host port")
				err = StopTask(cachedTask.ID, taskmap.TaskStartupFailure)
				if err != nil {
					l.WithError(err).Warn("Failed to stop task")
				}
				continue
			}

			cachedTask.Network = *starter.Env.Network
			err := taskmap.Write(cachedTask.ID, cachedTask, 0)
			if err != nil {
				l.WithError(fmt.Errorf("failed to recache task: %v", err)).Warn()
				err = StopTask(cachedTask.ID, taskmap.TaskStartupFailure)
				if err != nil {
					l.WithError(err).Warn("Failed to stop task")
				}
				continue
			}

			l.WithField("latency", time.Since(serviceStartupTime)).Debug("network environment set")
		}

		resp := make(map[string]interface{}, 0)
		resp["taskId"] = cachedTask.ID

		l.WithField("resp", resp).Debug("Response")
		l.WithField("latency", time.Since(serviceStartupTime)).Info("service started")
		return resp, nil
	}

	return nil, utils.CreationErr(fmt.Errorf("failed to start service"))
}

type commonStarter startBasis

func (starter commonStarter) StartService() (map[string]interface{}, *utils.SeleniumError) {
	serviceStartupTime := time.Now()
	ctx, ctxCancel := context.WithTimeout(context.Background(), config.Conf.ServiceStartupTimeout)
	defer ctxCancel()

	starter.Log.Info("service starting")
	for i := 0; true; i++ {
		l := starter.Log.WithField("attempt", i)

		// 1. register phase
		var taskArn string
		essentialErrCh, nonEsentialErrCh, taskArnCh := waitForPhase(ctx, startBasis(starter).registerTask)
		select {
		case <-ctx.Done():
			l.WithField("latency", time.Since(serviceStartupTime)).Info("service startup timed out")
			return nil, utils.CreationErr(fmt.Errorf("service startup timed out"))
		case err := <-essentialErrCh:
			l.WithField("latency", time.Since(serviceStartupTime)).WithError(err).Info("Got esential error, stopping service starter")
			return nil, utils.CreationErr(err)
		case err := <-nonEsentialErrCh:
			l.WithField("latency", time.Since(serviceStartupTime)).WithError(err).Warn("Got non esential error")
			continue
		case taskArn = <-taskArnCh:
			//got task arn
		}

		taskId := strings.Split(taskArn, "/")[2]
		l = l.WithField("_taskId", taskId)

		cachedTask, err := taskmap.CreateEntity(taskId, starter.Env)
		if err != nil {
			l.WithError(fmt.Errorf("failed to cache task: %v", err)).Warn()
			err := StopTask(cachedTask.ID, taskmap.TaskStartupFailure)
			if err != nil {
				l.WithError(err).Warn("Failed to stop task")
			}
			continue
		}

		// 2. start phase
		l.Info("task starting")
		var task *ecs.Task
		taskReq := taskWaiter.waitFor(ctx, taskArn)
		select {
		case <-taskReq.ctx.Done():
			l.WithField("latency", time.Since(serviceStartupTime)).Info("service startup timed out")
			err = StopTask(cachedTask.ID, taskmap.TaskStartupFailure)
			if err != nil {
				l.WithError(err).Warn("Failed to stop task")
			}
			return nil, utils.CreationErr(fmt.Errorf("service startup timed out"))
		case err := <-taskReq.nonEssentialErrCh:
			l.WithField("latency", time.Since(serviceStartupTime)).WithError(fmt.Errorf("failed to run task: %v", err)).Warn()
			err = StopTask(cachedTask.ID, taskmap.TaskStartupFailure)
			if err != nil {
				l.WithError(err).Warn("Failed to stop task")
			}
			continue
		case task = <-taskReq.responseChan:
			// timediff between HealthAt (current time) and task.startedAt should be cut during resources tracking to bill only actual (net) time
			cachedTask.HealthAt = time.Now()
			cachedTask.Status = taskmap.TaskActive
			err = taskmap.Write(cachedTask.ID, cachedTask, 0)
			if err != nil {
				l.WithError(fmt.Errorf("failed to recache task: %v", err)).Warn()
				err := StopTask(cachedTask.ID, taskmap.TaskStartupFailure)
				if err != nil {
					l.WithError(err).Warn("Failed to stop task")
				}
				continue
			}
			l.WithField("latency", time.Since(serviceStartupTime)).Info("task started")
		}

		// 3. network configuration phase
		l.Debug("setting network environment")
		instanceReq := instanceWorker.waitForInstance(ctx, task)
		select {
		case <-instanceReq.ctx.Done():
			l.WithField("latency", time.Since(serviceStartupTime)).Info("service startup timed out")
			err = StopTask(cachedTask.ID, taskmap.TaskStartupFailure)
			if err != nil {
				l.WithError(err).Warn("Failed to stop task")
			}
			return nil, utils.CreationErr(fmt.Errorf("service startup timed out"))
		case err := <-instanceReq.errorChan:
			l.WithField("latency", time.Since(serviceStartupTime)).WithError(err).Warn("Failed to get instance")
			err = StopTask(cachedTask.ID, taskmap.TaskStartupFailure)
			if err != nil {
				l.WithError(err).Warn("Failed to stop task")
			}
			continue
		case instance := <-instanceReq.responseChan:
			if config.Conf.UsePublicIp {
				starter.Env.Network.IP = *instance.PublicIpAddress
			} else {
				starter.Env.Network.IP = *instance.PrivateIpAddress
			}

			err = startBasis(starter).setHostPort(task)
			if err != nil {
				l.WithField("latency", time.Since(serviceStartupTime)).WithError(err).Warn("failed to set host port")
				err = StopTask(cachedTask.ID, taskmap.TaskStartupFailure)
				if err != nil {
					l.WithError(err).Warn("Failed to stop task")
				}
				continue
			}

			cachedTask.Network = *starter.Env.Network
			err := taskmap.Write(cachedTask.ID, cachedTask, 0)
			if err != nil {
				l.WithError(fmt.Errorf("failed to recache task: %v", err)).Warn()
				err = StopTask(cachedTask.ID, taskmap.TaskStartupFailure)
				if err != nil {
					l.WithError(err).Warn("Failed to stop task")
				}
				continue
			}
			l.WithField("latency", time.Since(serviceStartupTime)).Debug("network environment set")
		}

		// 4. driver start
		l.WithField("latency", time.Since(serviceStartupTime)).Info("driver started")
		u, ok := starter.Env.Network.GetUrl("driver")
		if !ok {
			l.Error("failed to get url for `driver` service")
			err = StopTask(cachedTask.ID, taskmap.SessiongStartupFailure)
			if err != nil {
				l.WithError(err).Warn("Failed to stop task")
			}
			continue
		}

		requestBody, err := json.Marshal(starter.ReqCaps)
		if err != nil {
			l.WithError(err).Error("Failed to marshal request")
			err = StopTask(cachedTask.ID, taskmap.SessiongStartupFailure)
			if err != nil {
				l.WithError(err).Warn("Failed to stop task")
			}
			continue
		}

		reqUrl := &url.URL{}
		reqUrl.Host, reqUrl.Path = u.Host, path.Join(u.Path, starter.GinCtx.Request.URL.Path)
		reqUrl.Scheme = "http"
		l.WithField("serviceUrl", reqUrl).Debug("driver starting")
		resp, err := selenium.StartSession(ctx, reqUrl, starter.GinCtx.Request.Header, requestBody)
		if err != nil {
			l.WithError(err).WithField("response", resp).Error("driver startup failure")
			err = StopTask(cachedTask.ID, taskmap.SessiongStartupFailure)
			if err != nil {
				l.WithError(err).Warn("Failed to stop task")
			}
			continue
		}

		sessionId, err := getSessionId(resp)
		if sessionId == "" {
			if err == nil {
				err = errors.New("session id in driver response is empty")
			}
			l.WithError(err).Error("Failed to get sessionId")
			err = StopTask(cachedTask.ID, taskmap.SessiongStartupFailure)
			if err != nil {
				l.WithError(err).Warn("Failed to stop task")
			}
			continue
		}

		cachedSession, err := sessionmap.CreateEntity(sessionId, starter.Env, cachedTask)
		if err != nil {
			l.WithError(err).Error("Failed to cache driver session")

			err = StopTask(cachedTask.ID, taskmap.SessiongStartupFailure)
			if err != nil {
				l.WithError(err).Warn("Failed to stop task")
			}
			continue
		}

		l.WithField("sessionId", cachedSession.ID).WithField("latency", time.Since(serviceStartupTime)).Info("driver started")
		l.WithField("resp", resp).Debug("Response")
		l.WithField("latency", time.Since(serviceStartupTime)).Info("service started")
		return resp, nil
	}

	return nil, utils.CreationErr(fmt.Errorf("failed to start service"))
}

func GetStarter(env *environment.ExecutionEnvironment, c *gin.Context, reqCaps *capabilities.RequestCaps, l *log.Entry) ServiceStarter {
	basis := startBasis{
		Env:     env,
		GinCtx:  c,
		ReqCaps: reqCaps,
		Log:     l,
		// PhaseChain: make([]phase, 0),
	}

	pm := phaseChainer[string, *ecs.Task]{
		waitForPhase: func(ctx context.Context, p phase[string]) *ecs.Task {
			essentialErrCh := make(chan error)
			nonEsentialErrCh := make(chan error)
			taskArnCh := make(chan string)
			go p(ctx, essentialErrCh, nonEsentialErrCh, taskArnCh)
			return nil
		},
	}

	if strings.Contains(env.TaskDefinitionFamily, "generic") {
		return genericStarter(basis)
	} else if strings.Contains(env.TaskDefinitionFamily, "cypress") {
		return cypressStarter(basis)
	} else {
		return commonStarter(basis)
	}
}

func waitForPhase(ctx context.Context, waitFn phase[string]) (<-chan error, <-chan error, <-chan string) {
	essentialErrCh := make(chan error)
	nonEsentialErrCh := make(chan error)
	taskArnCh := make(chan string)

	go waitFn(ctx, essentialErrCh, nonEsentialErrCh, taskArnCh)

	return essentialErrCh, nonEsentialErrCh, taskArnCh
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

func getSessionId(resp map[string]interface{}) (string, error) {
	// Get sessionId from root. For unknown reason opera returns sessionId in root of object
	sessionId, ok := resp["sessionId"].(string)
	if ok {
		return sessionId, nil
	}

	// Get session from value
	value, ok := resp["value"].(map[string]interface{})
	if !ok {
		return "", errors.New("`value` must be an object")
	}

	sessionId, ok = value["sessionId"].(string)
	if ok {
		return sessionId, nil
	}

	return "", errors.New("failed to find sessionId field in response")
}
