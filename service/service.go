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
	StartService(context.Context) (map[string]interface{}, *utils.SeleniumError)
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
	CachedTask   *taskmap.Task
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
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), time.Duration(10)*time.Second)
			defer cancel()
			select {
			case <-ctx.Done():
				return
			case taskArn := <-waitRequest.ResponseCh:
				taskId := strings.Split(taskArn, "/")[2]
				log.WithField(config.TaskIdKey, taskId).Warn("Task registered after context is done")
				StopTaskForcibly(taskId, taskmap.TaskStartupFailure)
				return
			}
		}()
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

		var err error
		for {
			s.CachedTask, err = taskmap.CreateEntity(taskId, s.Env)
			if err == nil {
				break
			}
			s.Log.WithError(err).Error("Failed to cach task")
			time.Sleep(5 * time.Second)
			if ctx.Err() != nil {
				nonEssential = err
				return
			}
		}

		// add task to ctx, so we can add taskId to selenium err log if any failure will happen later
		s.GinCtx.Set(config.TaskIdKey, s.CachedTask)

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
		healthyTime := time.Now()
		s.CachedTask.HealthAt = &healthyTime
		s.CachedTask.Status = taskmap.TaskActive

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

		s.CachedTask.Network = *s.Env.Network

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
		s.Log.WithError(essential).Warn("Failed to start driver, stopping service...")
		return
	}

	reqUrl := &url.URL{}
	reqUrl.Host, reqUrl.Path = u.Host, path.Join(u.Path, s.Request.URL.Path)
	reqUrl.Scheme = "http"
	s.Log = s.Log.WithField("driver url", reqUrl)

	startSessionRequest, err := http.NewRequest(http.MethodPost, reqUrl.String(), requestBody)
	if err != nil {
		essential = utils.CreationErr(fmt.Errorf("failed to start driver"), err.Error())
		s.Log.WithError(essential).Warn("Failed to start driver, stopping service...")
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
		s.Log.WithField("latency", time.Since(s.ServiceStart)).WithError(essentialReason).Info("Failed to start driver, stopping service...")
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
		for {
			sess, err = sessionmap.CreateEntity(sessionId, s.Env, s.TaskId)
			if err == nil {
				break
			}
			s.Log.WithError(err).Error("Failed to cach session")
			time.Sleep(5 * time.Second)
			if ctx.Err() != nil {
				nonEssential = err
				return
			}
		}
		s.CachedTask.CurrentSessionID = sessionId

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

func (starter genericStarter) StartService(startupTime context.Context) (map[string]interface{}, *utils.SeleniumError) {
	//override request context, as after response is sent, request context is canceled
	starter.basis.Request = starter.basis.Request.WithContext(context.Background())
	go func() {
		// create new task definition for generic task
		taskDefinition, err := CreateTaskDefinition(starter.basis.Env)
		// abort launch if failed to create new task definition
		if err != nil {
			log.WithError(err).Error("Failed to create task definition")
			zebrunner.AbortLaunch(starter.basis.Env.RouterUUID, starter.basis.Env.Workspace,
				starter.basis.Env.Capabilities.LaunchUUID.ToPrimitive(), fmt.Sprintf("failed to create task defenition for generic: %v", err.Error()))
			return
		}
		// set revision of newly created task definition
		starter.basis.Env.TaskDefinitionFamily = fmt.Sprintf("%s:%v", starter.basis.Env.TaskDefinitionFamily, *taskDefinition.Revision)

		_, startErr := basicStarter(starter).StartService(startupTime)

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

func (starter basicStarter) StartService(startupTime context.Context) (map[string]interface{}, *utils.SeleniumError) {
	starter.basis.ServiceStart = time.Now()
	starter.basis.Log.Info("service starting")

	if err := mapper.InitEntity(starter.basis.Env.RouterUUID); err != nil {
		return nil, utils.CreationErr(fmt.Errorf("service startup failed"), err.Error())
	}

	for i := 0; true; i++ {
		logCopy := *starter.basis.Log
		starter.basis.Log = starter.basis.Log.WithField("attempt", i)
		success := true

		for _, phase := range starter.basis.Phases {
			essential, nonEssential := phase(startupTime)
			if starter.basis.Request.Context().Err() != nil {
				// stop service starter, return error
				if starter.basis.CachedTask != nil {
					StopTask(*starter.basis.CachedTask, taskmap.TaskStartupFailure)
				}
				seErr := utils.CreationErr(fmt.Errorf("service start has been canceled"))
				return nil, seErr
			}

			if essential != nil {
				// stop service starter, return error
				if starter.basis.CachedTask != nil {
					StopTask(*starter.basis.CachedTask, taskmap.TaskStartupFailure)
				}
				return nil, essential
			}

			if nonEssential != nil {
				// flush data, next retry
				if starter.basis.CachedTask != nil {
					// check abort status in case of non esential error
					task, err := taskmap.Find(starter.basis.CachedTask.TaskId, false)
					if err == nil && task.StopReason == taskmap.TaskAborted {
						// stop service starter, return error
						seErr := utils.CreationErr(fmt.Errorf("service start has been aborted"))
						return nil, seErr
					}
					StopTask(*starter.basis.CachedTask, taskmap.TaskStartupFailure)
				}
				starter.basis.Log = &logCopy
				starter.basis.Task = nil
				starter.basis.TaskId = nil
				starter.basis.CachedTask = nil
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
				s.CachedTask.Status = taskmap.TaskGeneric
				responseCh, errCh := taskmap.UpdateTask(*s.CachedTask, 0)
				select {
				case err := <-errCh:
					basis.Log.WithError(err).Error("Failed to recache task on finalize!")
				case <-responseCh:
				}
			},
		}
	} else if strings.Contains(env.TaskDefinitionFamily, "cypress") {
		basis.appendPhase(basis.registerTaskPhase).appendPhase(basis.startTaskPhase).appendPhase(basis.setNetworkPhase)

		starter = basicStarter{
			basis: basis,
			finalizeFunc: func(s *startBasis) {
				s.CachedTask.Status = taskmap.TaskGeneric
				s.CachedTask.AccessedAt = time.Now()
				responseCh, errCh := taskmap.UpdateTask(*s.CachedTask, 0)
				select {
				case err := <-errCh:
					basis.Log.WithError(err).Error("Failed to recache task on finalize!")
				case <-responseCh:
				}
				taskmap.AddToCypressSet(s.CachedTask.TaskId)
			},
		}
	} else {
		basis.appendPhase(basis.registerTaskPhase).appendPhase(basis.startTaskPhase).appendPhase(basis.setNetworkPhase).appendPhase(basis.startDriverPhase)
		starter = basicStarter{
			basis: basis,
			finalizeFunc: func(s *startBasis) {
				//cache all collected data during startup
				responseCh, errCh := taskmap.UpdateTask(*s.CachedTask, 0)
				select {
				case err := <-errCh:
					basis.Log.WithError(err).Error("Failed to recache task on finalize!")
				case <-responseCh:
				}
			},
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
