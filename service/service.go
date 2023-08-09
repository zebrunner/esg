package service

import (
	"context"
	"net/http"
	"strings"
	"time"

	log "github.com/sirupsen/logrus"
	"github.com/zebrunner/esg/capabilities"
	"github.com/zebrunner/esg/config"
	"github.com/zebrunner/esg/environment"
	"github.com/zebrunner/esg/utils"
)

// essential errors
var (
	ImageNotFound  = "image not found"
	ContextTimeout = context.DeadlineExceeded.Error()
)

type ServiceStarter interface {
	StartService() (map[string]interface{}, *utils.SeleniumError)
}

type startBasis struct {
	Env     *environment.ExecutionEnvironment
	Request *http.Request
	ReqCaps *capabilities.RequestCaps
	Log     *log.Entry
}

func (sb startBasis) waitForTaskRegister(ctx context.Context) (essentialErrCh <-chan error, nonEsentialErrCh <-chan error, taskArnCh <-chan string) {
	essentialErrCh = make(chan error)
	nonEsentialErrCh = make(chan error)
	taskArnCh = make(chan string)

	go sb.registerTask(ctx, essentialErrCh, nonEsentialErrCh, taskArnCh)

	return
}

func (sb startBasis) registerTask(ctx context.Context, essentialErrCh chan<- error, nonEsentialErrCh chan<- error, taskArnCh chan<- string) {

}

type genericStarter startBasis

func (starter genericStarter) StartService() (map[string]interface{}, *utils.SeleniumError) {
	serviceStartupTime := time.Now()
	ctx, ctxCancel := context.WithTimeout(context.Background(), config.Conf.ServiceStartupTimeout)
	defer ctxCancel()

	for i := 0; true; i++ {
		l := starter.Log.WithField("attempt", i)
		l.Info("service starting")

		var taskId string
		essentialErrCh, nonEsentialErrCh, taskArnCh := startBasis(starter).waitForTaskRegister(ctx)
		select {
		case <-ctx.Done():
			l.Info("context done")
			continue
		case err := <-essentialErrCh:
			return nil, utils.CreationErr(err)
		case err := <-nonEsentialErrCh:
			l.WithError(err).Error()
			continue
		case taskArn := <-taskArnCh:
			taskId = strings.Split(taskArn, "/")[2]
			l = l.WithField("_taskId", taskId)
		}
		cachedTask, task, err := StartTask(ctx, env)
		//return error on task startup failure
		if err != nil {
			l.Errorf("service startup failed: %v", err)
			c.Error(utils.CreationErr(err)).SetType(gin.ErrorTypePublic)
			return
		}

		l = l.WithField("_taskId", cachedTask.ID)
		resp := make(map[string]interface{}, 0)

		// return response for generic task as soon as possible.
		if strings.Contains(env.TaskDefinitionFamily, "generic") {
			resp["taskId"] = cachedTask.ID
			l.WithFields(log.Fields{"resp": resp}).Debug("Response")
			c.JSON(http.StatusOK, resp)
			return
		}

		setNetworkStartTime := time.Now()
		err = service.SetEnvironmentNetwork(ctx, env, task)
		if err != nil {
			l.WithField("latency", time.Since(setNetworkStartTime)).WithError(err).Warn("failed to set network info")
			err = service.StopTask(cachedTask.ID, taskmap.SessiongStartupFailure)
			if err != nil {
				l.WithError(err).Warn("Failed to stop task")
			}
			continue
		}
		l.WithField("latency", time.Since(setNetworkStartTime)).Debug("network info set")

		// return response for cypress task after network env is set
		if strings.Contains(env.TaskDefinitionFamily, "cypress") {
			resp := make(map[string]interface{}, 0)
			resp["taskId"] = cachedTask.ID
			cachedTask.Status = taskmap.TaskGeneric
			err = taskmap.Write(cachedTask.ID, cachedTask, 0)
			if err != nil {
				l.WithError(fmt.Errorf("failed to recache task: %v", err))
			}
			l.WithFields(log.Fields{"resp": resp}).Debug("Response")
			c.JSON(http.StatusOK, resp)
			return
		}

		// mark other tasks (except cypress and generic) as Active
		cachedTask.Status = taskmap.TaskActive
		err = taskmap.Write(cachedTask.ID, cachedTask, 0)
		if err != nil {
			l.WithError(fmt.Errorf("failed to recache task: %v", err))
		}

		u, ok := env.Network.GetUrl("driver")
		if !ok {
			l.Error("failed to get url for `driver` service")
			err = service.StopTask(cachedTask.ID, taskmap.SessiongStartupFailure)
			if err != nil {
				l.WithError(err).Warn("Failed to stop task")
			}
			continue
		}

		requestBody, err := json.Marshal(env.RawCapabilities)
		if err != nil {
			l.WithError(err).Error("Failed to marshal request")
			err = service.StopTask(cachedTask.ID, taskmap.SessiongStartupFailure)
			if err != nil {
				l.WithError(err).Warn("Failed to stop task")
			}
			continue
		}

		reqUrl := &url.URL{}
		reqUrl.Host, reqUrl.Path = u.Host, path.Join(u.Path, c.Request.URL.Path)
		reqUrl.Scheme = "http"
		l.WithField("serviceUrl", reqUrl).Debug("driver starting")
		driverResp, err := selenium.StartSession(ctx, reqUrl, c.Request.Header, requestBody)
		if err != nil {
			l.WithError(err).WithField("response", driverResp).Error("driver startup failure")
			err = service.StopTask(cachedTask.ID, taskmap.SessiongStartupFailure)
			if err != nil {
				l.WithError(err).Warn("Failed to stop task")
			}
			continue
		}

		sessionId, err := getSessionId(driverResp)
		if sessionId == "" {
			if err == nil {
				err = errors.New("session id in driver response is empty")
			}
			l.WithError(err).Error("Failed to get sessionId")
			err = service.StopTask(cachedTask.ID, taskmap.SessiongStartupFailure)
			if err != nil {
				l.WithError(err).Warn("Failed to stop task")
			}
			continue
		}

		cachedSession, err := sessionmap.CreateEntity(sessionId, env, cachedTask)
		if err != nil {
			l.WithError(err).Error("Failed to cache driver session")

			err = service.StopTask(cachedTask.ID, taskmap.SessiongStartupFailure)
			if err != nil {
				l.WithError(err).Warn("Failed to stop task")
			}
			continue
		}
	}
}

type cypressStarter startBasis

func (starter cypressStarter) StartService() (map[string]interface{}, *utils.SeleniumError) {
	sessionStartTime := time.Now()
	ctx, ctxCancel := context.WithTimeout(context.Background(), config.Conf.ServiceStartupTimeout)
	defer ctxCancel()
}

type commonStarter startBasis

func (starter commonStarter) StartService() (map[string]interface{}, *utils.SeleniumError) {
	sessionStartTime := time.Now()
	ctx, ctxCancel := context.WithTimeout(context.Background(), config.Conf.ServiceStartupTimeout)
	defer ctxCancel()

}

func GetStarter(env *environment.ExecutionEnvironment, req *http.Request, reqCaps *capabilities.RequestCaps, l *log.Entry) ServiceStarter {
	basis := startBasis{
		Env:     env,
		Request: req,
		ReqCaps: reqCaps,
		Log:     l,
	}

	if strings.Contains(env.TaskDefinitionFamily, "generic") {
		return genericStarter(basis)
	} else if strings.Contains(env.TaskDefinitionFamily, "cypress") {
		return cypressStarter(basis)
	} else {
		return commonStarter(basis)
	}
}

func StartTask(ctx context.Context, env *environment.ExecutionEnvironment) (*taskmap.Task, *ecs.Task, error) {
	var outputErr error
	startTime := time.Now()
	// retry attempt counter
out:
	for i := 0; true; i++ {
		l := log.WithField("taskStartAttempt", i)
		select {
		case <-ctx.Done():
			if outputErr == nil {
				outputErr = fmt.Errorf("error forwarding the new session request timed out waiting for a node to become available")
			}
			break out
		default:
		}

		taskArn, err := RegisterTask(ctx, env)
		if err != nil {
			outputErr = fmt.Errorf("failed to run task: %v", err)
			l.WithError(outputErr).WithField("latency", time.Since(startTime)).Warn()

			if strings.HasPrefix(err.Error(), "image not found: ") || strings.HasPrefix(err.Error(), "InvalidParameterException") { //#366 disable retries for InvalidParameterException
				break out
			}
			continue
		}

		taskId := strings.Split(taskArn, "/")[2]
		l = l.WithField("_taskId", taskId)

		//not waiting for generic task
		if strings.Contains(env.TaskDefinitionFamily, "generic") {
			cachedTask := &taskmap.Task{
				ID:           taskId,
				Capabilities: env.Capabilities,
				Status:       taskmap.TaskGeneric, // TODO: change status to active when CloseSession() for generic tasks will be called
				Workspace:    env.Workspace,
				HealthAt:     time.Now(), //TODO: remove HealthAt as only healthcheck integrated into the generic as well
				Network:      *env.Network,
			}

			err := taskmap.Write(cachedTask.ID, cachedTask, 0)
			if err != nil {
				outputErr = fmt.Errorf("failed to cache task: %v", err)
				l.WithError(outputErr).Warn()
				err := StopTask(cachedTask.ID, taskmap.TaskStartupFailure)
				if err != nil {
					l.WithError(err).Warn("Failed to stop task")
				}
				continue
			}

			l.Debug("do not wait for generic task startup.")
			return cachedTask, nil, nil
		}

		// caching task as soon as possible
		cachedTask, err := taskmap.CreateEntity(taskId, env)
		if err != nil {
			outputErr = fmt.Errorf("failed to cache task: %v", err)
			l.WithError(outputErr).Warn()
			err := StopTask(cachedTask.ID, taskmap.TaskStartupFailure)
			if err != nil {
				l.WithError(err).Warn("Failed to stop task")
			}
			continue
		}

		l.Info("task starting")
		req := taskWaiter.waitFor(ctx, taskArn)
		select {
		case <-req.ctx.Done():
			// don't close chans from receiver side
			// https://go.dev/tour/concurrency/4#:~:text=Note%3A%20Only%20the%20sender%20should,to%20terminate%20a%20range%20loop.
			l.WithField("latency", time.Since(startTime)).Warn("failed to wait until task is running. context deadline")
		case err := <-req.errorChan:
			outputErr = fmt.Errorf("failed to run task: %v", err)
			l.WithField("latency", time.Since(startTime)).WithError(outputErr).Warn()
		case task := <-req.responseChan:
			// timediff between HealthAt (current time) and task.startedAt should be cut during resources tracking to bill only actual (net) time
			cachedTask.HealthAt = time.Now()
			taskmap.Write(cachedTask.ID, cachedTask, 0)
			l.WithField("latency", time.Since(startTime)).Info("task started")
			return cachedTask, task, nil
		}
		// will be called only for unsuccess task startup
		// as on success startup we return from func in switch select
		err = StopTask(cachedTask.ID, taskmap.TaskStartupFailure)
		if err != nil {
			l.WithError(err).Warn("Failed to stop task")
		}
	}

	return nil, nil, outputErr
}
