package service

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go/service/ecs"
	"github.com/gin-gonic/gin"
	log "github.com/sirupsen/logrus"
	"github.com/zebrunner/esg/cachemaps/mapper"
	"github.com/zebrunner/esg/cachemaps/sessionmap"
	"github.com/zebrunner/esg/cachemaps/taskmap"
	"github.com/zebrunner/esg/config"
	"github.com/zebrunner/esg/environment"
	"github.com/zebrunner/esg/selenium"
	"github.com/zebrunner/esg/utils"
	"github.com/zebrunner/esg/zebrunner"
)

type ServiceStarter interface {
	StartService() (map[string]interface{}, *utils.SeleniumError)
}

type startBasis struct {
	ServiceStart time.Time
	Log          *log.Entry
	GinCtx       *gin.Context
	Request      *http.Request
	Env          *environment.ExecutionEnvironment
	Phases       []phase
	Task         *ecs.Task
	TaskId       *string
	Reply        map[string]interface{}
}

// essential error -> stop service, non essential error -> retry service start
type phase func(ctx context.Context) (*utils.SeleniumError, error)

func (s *startBasis) appendPhase(p phase) *startBasis {
	s.Phases = append(s.Phases, p)
	return s
}

func (s *startBasis) registerTaskPhase(ctx context.Context) (essential *utils.SeleniumError, nonEssential error) {
	s.Log.Debug("task registering")
	waitRequest := WaitForTaskRegister(ctx, *s.Env)
	select {
	case <-ctx.Done():
		s.Log.WithField("latency", time.Since(s.ServiceStart)).Info("Task register timed out")
		essential = utils.CreationErr(fmt.Errorf("service startup timed out"))
		return
	case essentialReason := <-waitRequest.EssentialErrCh:
		s.Log.WithField("latency", time.Since(s.ServiceStart)).WithError(essential).Info("Failed to register task, stopping service...")
		essential = utils.CreationErr(fmt.Errorf("failed to create task"), essentialReason.Error())
		return
	case nonEssential = <-waitRequest.NonEssentialErrCh:
		s.Log.WithField("latency", time.Since(s.ServiceStart)).WithError(nonEssential).Warn("Failed to register task, restarting...")
		return
	case taskArn := <-waitRequest.ResponseCh:
		taskId := strings.Split(taskArn, "/")[2]

		s.Log = s.Log.WithField(config.TaskIdKey, taskId)
		s.TaskId = &taskId

		var cachedTask *taskmap.Task
		cachedTask, nonEssential = taskmap.CreateEntity(taskId, s.Env)
		if nonEssential != nil {
			s.Log.WithError(nonEssential).Warn("Failed to cache task, restarting...")
			StopTaskForcibly(taskId, taskmap.TaskStartupFailure)
			return
		}

		// add task to ctx, so we can add taskId to selenium err log if any failure will happen later
		s.GinCtx.Set(config.TaskIdKey, cachedTask)

		s.Log.WithField("latency", time.Since(s.ServiceStart)).Debug("task registered")
		s.Reply = map[string]interface{}{"taskId": s.Env.RouterUUID}
		return nil, nil
	}
}

func (s *startBasis) startTaskPhase(ctx context.Context) (essential *utils.SeleniumError, nonEssential error) {
	s.Log.Info("task starting")
	waitRequest := taskWaiter.waitFor(ctx, *s.TaskId)
	select {
	case <-ctx.Done():
		s.Log.WithField("latency", time.Since(s.ServiceStart)).Info("Task startup timed out")
		essential = utils.CreationErr(fmt.Errorf("request timed out waiting for a node to become available"))
		return
	case essentialReason := <-waitRequest.EssentialErrCh:
		essential = utils.CreationErr(fmt.Errorf("failed to start task"), essentialReason.Error())
		s.Log.WithField("latency", time.Since(s.ServiceStart)).WithError(essential).Info("Failed to start task, stopping service...")
		return
	case nonEssential = <-waitRequest.NonEssentialErrCh:
		s.Log.WithField("latency", time.Since(s.ServiceStart)).WithError(nonEssential).Warn("Failed to start task, restarting...")
		return
	case s.Task = <-waitRequest.ResponseCh:
		cachedTask, err := taskmap.Find(*s.TaskId, false)
		if err != nil {
			s.Log.WithError(nonEssential).Warn("Failed to find task's cache on task start phase, restarting...")
			nonEssential = err
			return
		}

		cachedTask.HealthAt = time.Now()
		cachedTask.Status = taskmap.TaskActive
		nonEssential = taskmap.Write(cachedTask.TaskId, cachedTask, -1)
		if nonEssential != nil {
			s.Log.WithError(nonEssential).Warn("Failed to recache task on task start phase, restarting...")
			return
		}

		s.Log.WithField("latency", time.Since(s.ServiceStart)).Info("task started")
		s.Reply = map[string]interface{}{"taskId": s.Env.RouterUUID}
		return nil, nil
	}
}

func (s *startBasis) setNetworkPhase(ctx context.Context) (essential *utils.SeleniumError, nonEssential error) {
	s.Log.Debug("setting network environment")
	waitRequest := instanceWorker.waitForInstance(ctx, s.Task)
	select {
	case <-ctx.Done():
		s.Log.WithField("latency", time.Since(s.ServiceStart)).Info("Network configure timed out")
		essential = utils.CreationErr(fmt.Errorf("service startup timed out"))
		return
	case essentialReason := <-waitRequest.EssentialErrCh:
		s.Log.WithField("latency", time.Since(s.ServiceStart)).WithError(essential).Info("Failed to get network configuration, stopping service...")
		essential = utils.CreationErr(fmt.Errorf("failed to set network configuration"), essentialReason.Error())
		return
	case nonEssential = <-waitRequest.NonEssentialErrCh:
		s.Log.WithField("latency", time.Since(s.ServiceStart)).WithError(nonEssential).Warn("Failed to get Network configuration, restarting...")
		return
	case instance := <-waitRequest.ResponseCh:
		if config.Conf.UsePublicIp {
			s.Env.Network.IP = *instance.PublicIpAddress
		} else {
			s.Env.Network.IP = *instance.PrivateIpAddress
		}

		nonEssential = s.setHostPort()
		if nonEssential != nil {
			s.Log.WithField("latency", time.Since(s.ServiceStart)).WithError(nonEssential).Warn("failed to set host port")
			return
		}

		cachedTask, err := taskmap.Find(*s.TaskId, false)
		if err != nil {
			s.Log.WithError(nonEssential).Warn("Failed to find task's cache on network phase, restarting...")
			nonEssential = err
			return
		}

		cachedTask.Network = *s.Env.Network
		nonEssential = taskmap.Write(cachedTask.TaskId, cachedTask, -1)
		if nonEssential != nil {
			s.Log.WithError(nonEssential).Warn("Failed to cache task on network phase, restarting...")
			return
		}

		s.Log.WithField("latency", time.Since(s.ServiceStart)).Info("network environment set")

		s.Reply = map[string]interface{}{"taskId": s.Env.RouterUUID}
		return nil, nil
	}
}

func (s *startBasis) startDriverPhase(ctx context.Context) (essential *utils.SeleniumError, nonEssential error) {
	s.Log.Info("driver starting")

	u, ok := s.Env.Network.GetUrl("driver")
	if !ok {
		nonEssential = fmt.Errorf("failed to get driver network")
		s.Log.WithError(nonEssential).Warn("Failed to start driver, restarting...")
		return
	}

	requestBody, err := s.Env.ReqCapabilities.ToRequestBody()
	if err != nil {
		essential = utils.CreationErr(fmt.Errorf("failed to start driver"), err.Error())
		s.Log.WithError(nonEssential).Warn("Failed to start driver, stopping service...")
		return
	}

	reqUrl := &url.URL{}
	reqUrl.Host, reqUrl.Path = u.Host, path.Join(u.Path, s.Request.URL.Path)
	reqUrl.Scheme = "http"
	s.Log = s.Log.WithField("driver url", reqUrl)

	startSessionRequest, err := http.NewRequest(http.MethodPost, reqUrl.String(), requestBody)
	if err != nil {
		essential = utils.CreationErr(fmt.Errorf("failed to start driver"), err.Error())
		s.Log.WithError(nonEssential).Warn("Failed to start driver, stopping service...")
		return
	}

	startSessionRequest.Header = s.Request.Header

	waitRequest := selenium.WaitForSessionStart(ctx, startSessionRequest)
	select {
	case <-ctx.Done():
		s.Log.WithField("latency", time.Since(s.ServiceStart)).Info("driver startup timed out")
		essential = utils.CreationErr(fmt.Errorf("service startup timed out"))
		return
	case essentialReason := <-waitRequest.EssentialErrCh:
		s.Log.WithField("latency", time.Since(s.ServiceStart)).WithError(essential).Info("Failed to start driver, stopping service...")
		essential = utils.CreationErr(fmt.Errorf("failed to start driver"), essentialReason.Error())
		return
	case nonEssential = <-waitRequest.NonEssentialErrCh:
		s.Log.WithField("latency", time.Since(s.ServiceStart)).WithError(nonEssential).Warn("Failed to start driver, restarting...")
		return
	case s.Reply = <-waitRequest.ResponseCh:
		var sessionId string
		// replace sessionId from driver response with router uuid
		sessionId, nonEssential = replaceSessionId(s.Reply, s.Env.RouterUUID)
		if sessionId == "" {
			if nonEssential == nil {
				nonEssential = fmt.Errorf("session id in driver response is empty")
			}
			s.Log.WithError(err).Error("Failed to get sessionId")
			return
		}

		s.Log = s.Log.WithField(config.SessionIdKey, sessionId)

		var sess *sessionmap.Session
		sess, nonEssential = sessionmap.CreateEntity(sessionId, s.Env, s.TaskId)
		if err != nil {
			s.Log.WithError(err).Error("Failed to cache driver session")
			return
		}
		// add session to ctx, so we can add it to selenium err log if any failure will happen later
		s.GinCtx.Set(config.SessionIdKey, sess)

		s.Log.WithField("latency", time.Since(s.ServiceStart)).Info("driver started")
		return nil, nil
	}
}

func (s *startBasis) setHostPort() error {
	if s.Task == nil {
		return fmt.Errorf("task is nil")
	}

	for _, endpoint := range s.Env.Network.Endpoints {
		hostPort, ok := searchHostPort(s.Task, endpoint.ContainerPort)
		if !ok {
			return fmt.Errorf("host port not found. containerPort=%d", endpoint.ContainerPort)
		}
		endpoint.HostPort = hostPort
	}

	return nil
}

type genericStarter struct {
	basis        *startBasis
	finalizeFunc func(basis *startBasis)
}

func (starter genericStarter) StartService() (map[string]interface{}, *utils.SeleniumError) {
	//override request context, as after response is sent, request context is canceled
	starter.basis.Request = starter.basis.Request.WithContext(context.Background())
	go func() {
		_, startErr := basicStarter(starter).StartService()

		// abort launch if service startup returned error
		if startErr != nil {
			zebrunner.AbortLaunch(starter.basis.Env.RouterUUID, starter.basis.Env.Workspace,
				starter.basis.Env.Capabilities.LaunchUUID.ToPrimitive(), startErr.Error())
		}
	}()

	return gin.H{"taskId": starter.basis.Env.RouterUUID}, nil
}

type basicStarter struct {
	basis        *startBasis
	finalizeFunc func(basis *startBasis)
}

func (starter basicStarter) finalize() {
	if starter.finalizeFunc != nil {
		starter.finalizeFunc(starter.basis)
	}
}

func (starter basicStarter) StartService() (map[string]interface{}, *utils.SeleniumError) {
	ctx, ctxCancel := context.WithTimeout(context.Background(), config.Conf.ServiceStartupTimeout)
	defer ctxCancel()

	starter.basis.ServiceStart = time.Now()
	starter.basis.Log.Info("service starting")

	if err := mapper.InitEntity(starter.basis.Env.RouterUUID); err != nil {
		return nil, utils.CreationErr(fmt.Errorf("service startup failed"), err.Error())
	}

	for i := 0; true; i++ {
		logCopy := *starter.basis.Log
		starter.basis.Log = starter.basis.Log.WithField("attempt", i)
		success := true

		for _, p := range starter.basis.Phases {
			essential, nonEssential := p(ctx)

			// check context/abort status before any error validation
			task, err := taskmap.FindByRouterUUID(starter.basis.Env.RouterUUID)
			if err == nil && task != nil && task.StopReason == taskmap.TaskAborted {
				// stop service starter, return error
				seErr := utils.CreationErr(fmt.Errorf("service start has been aborted"))
				return nil, seErr
			}

			if starter.basis.Request.Context().Err() != nil {
				// stop service starter, return error
				if starter.basis.TaskId != nil {
					StopTask(*starter.basis.TaskId, taskmap.TaskStartupFailure)
				}
				seErr := utils.CreationErr(fmt.Errorf("service start has been canceled"))
				return nil, seErr
			}

			if essential != nil {
				// stop service starter, return error
				if starter.basis.TaskId != nil {
					StopTask(*starter.basis.TaskId, taskmap.TaskStartupFailure)
				}
				return nil, essential
			}

			if nonEssential != nil {
				// flush data, next retry
				if starter.basis.TaskId != nil {
					StopTask(*starter.basis.TaskId, taskmap.TaskStartupFailure)
				}
				starter.basis.Log = &logCopy
				starter.basis.Task = nil
				starter.basis.TaskId = nil
				starter.basis.GinCtx.Set(config.TaskIdKey, "")
				starter.basis.GinCtx.Set(config.SessionIdKey, "")
				// flag for retries execution
				success = false
				break
			}
		}

		if success {
			// all phases executed, no errors, finalize service start and return reply
			starter.finalize()
			starter.basis.Log.Info("service started")
			return starter.basis.Reply, nil
		}
	}

	return nil, utils.UnknownErr(fmt.Errorf("service startup failed"))
}

func GetServiceStarter(env *environment.ExecutionEnvironment, c *gin.Context, l *log.Entry) ServiceStarter {
	basis := &startBasis{
		Log:     l,
		GinCtx:  c,
		Request: c.Request,
		Env:     env,
		Phases:  make([]phase, 0),
		Reply:   make(map[string]interface{}, 0),
	}

	var starter ServiceStarter
	if strings.Contains(env.TaskDefinitionFamily, "generic") {
		basis.appendPhase(basis.registerTaskPhase).appendPhase(basis.startTaskPhase)

		starter = genericStarter{
			basis: basis,
			finalizeFunc: func(s *startBasis) {
				cachedTask, err := taskmap.Find(*s.TaskId, false)
				if err != nil {
					log.WithError(err).Error("Failed to find task cache on finalize!")
				}
				cachedTask.Status = taskmap.TaskGeneric
				err = taskmap.Write(cachedTask.TaskId, cachedTask, -1)
				if err != nil {
					log.WithError(err).Error("Failed to recache task on finalize!")
				}
			},
		}
	} else if strings.Contains(env.TaskDefinitionFamily, "cypress") {
		basis.appendPhase(basis.registerTaskPhase).appendPhase(basis.startTaskPhase).appendPhase(basis.setNetworkPhase)

		starter = basicStarter{
			basis: basis,
			finalizeFunc: func(s *startBasis) {
				cachedTask, err := taskmap.Find(*s.TaskId, false)
				if err != nil {
					log.WithError(err).Error("Failed to find task cache on finalize!")
				}
				cachedTask.Status = taskmap.TaskGeneric
				cachedTask.AccessedAt = time.Now()
				err = taskmap.Write(cachedTask.TaskId, cachedTask, -1)
				if err != nil {
					log.WithError(err).Error("Failed to recache task on finalize!")
				}
				taskmap.AddToCypressSet(cachedTask.TaskId)
			},
		}
	} else {
		basis.appendPhase(basis.registerTaskPhase).appendPhase(basis.startTaskPhase).appendPhase(basis.setNetworkPhase).appendPhase(basis.startDriverPhase)

		starter = basicStarter{
			basis: basis,
		}
	}

	return starter
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

// replaceSessionId returns actual driver's session id and changes it value in driverResponse map with router uuid.
func replaceSessionId(driverResponse map[string]interface{}, routerUUID string) (string, error) {
	// Get driverSessionId from root. For unknown reason opera returns driverSessionId in root of object
	driverSessionId, ok := driverResponse["sessionId"].(string)
	if ok {
		// if driverSessionId found, change it in response map with router UUID
		driverResponse["sessionId"] = routerUUID
		// return actual driverSessionId
		return driverSessionId, nil
	}

	// Get session from value
	value, ok := driverResponse["value"].(map[string]interface{})
	if !ok {
		return "", fmt.Errorf("`value` must be an object")
	}

	driverSessionId, ok = value["sessionId"].(string)
	if ok {
		// if driverSessionId found, change it in response map with router UUID
		value["sessionId"] = routerUUID
		// return actual driverSessionId
		return driverSessionId, nil
	}

	return "", fmt.Errorf("failed to find sessionId field in response")
}
