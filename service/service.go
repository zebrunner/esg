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
	CachedTask   *taskmap.Task
	Task         *ecs.Task
}

// essential error -> stop service, non essential error -> retry service start, response chan -> successfull phase execution
type phase func(ctx context.Context) (map[string]interface{}, *utils.SeleniumError, error)

func (s *startBasis) appendPhase(p phase) *startBasis {
	s.Phases = append(s.Phases, p)
	return s
}

func (s *startBasis) registerTaskPhase(ctx context.Context) (reply map[string]interface{}, essential *utils.SeleniumError, nonEssential error) {
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

		s.CachedTask, nonEssential = taskmap.CreateEntity(taskId, s.Env)
		if nonEssential != nil {
			s.Log.WithError(nonEssential).Warn("Failed to cache task, restarting...")
			StopTaskForcibly(taskId, taskmap.TaskStartupFailure)
			return
		}
		// moved here as cancel on this phase still may produce healthy lost task
		if s.Request.Context().Err() != nil {
			essential = utils.CreationErr(fmt.Errorf("create request is canceled or timed out"))
			s.Log.WithField("latency", time.Since(s.ServiceStart)).WithError(essential).Info("Failed to register task, stopping service...")
			return
		}

		// add task to ctx, so we can add taskId to selenium err log if any failure will happen later
		s.GinCtx.Set(config.TaskIdKey, s.CachedTask)

		s.Log.WithField("latency", time.Since(s.ServiceStart)).Debug("task registered")
		reply = make(map[string]interface{}, 0)
		reply["taskId"] = s.Env.UUID
		return reply, nil, nil
	}
}

func (s *startBasis) startTaskPhase(ctx context.Context) (reply map[string]interface{}, essential *utils.SeleniumError, nonEssential error) {
	s.Log.Info("task starting")
	waitRequest := taskWaiter.waitFor(ctx, s.CachedTask.TaskId)
	select {
	case <-s.Request.Context().Done():
		essential = utils.CreationErr(fmt.Errorf("create request is canceled or timed out"))
		s.Log.WithField("latency", time.Since(s.ServiceStart)).WithError(essential).Info("Failed to start task, stopping service...")
		return
	case <-ctx.Done():
		s.Log.WithField("latency", time.Since(s.ServiceStart)).Info("Task startup timed out")
		essential = utils.CreationErr(fmt.Errorf("request timed out waiting for a node to become available"))
		return
	case essentialReason := <-waitRequest.EssentialErrCh:
		s.Log.WithField("latency", time.Since(s.ServiceStart)).WithError(essential).Info("Failed to start task, stopping service...")
		essential = utils.CreationErr(fmt.Errorf("failed to start task"), essentialReason.Error())
		return
	case nonEssential = <-waitRequest.NonEssentialErrCh:
		s.Log.WithField("latency", time.Since(s.ServiceStart)).WithError(nonEssential).Warn("Failed to start task, restarting...")
		return
	case s.Task = <-waitRequest.ResponseCh:
		s.CachedTask.HealthAt = time.Now()
		s.CachedTask.Status = taskmap.TaskActive
		nonEssential = taskmap.Write(s.CachedTask.TaskId, s.CachedTask, 0)
		if nonEssential != nil {
			s.Log.WithError(nonEssential).Warn("Failed to cache task, restarting...")
			return
		}

		s.Log.WithField("latency", time.Since(s.ServiceStart)).Info("task started")
		reply = make(map[string]interface{}, 0)
		reply["taskId"] = s.Env.UUID
		return reply, nil, nil
	}
}

func (s *startBasis) setNetworkPhase(ctx context.Context) (reply map[string]interface{}, essential *utils.SeleniumError, nonEssential error) {
	s.Log.Debug("setting network environment")
	waitRequest := instanceWorker.waitForInstance(ctx, s.Task)
	select {
	case <-s.Request.Context().Done():
		essential = utils.CreationErr(fmt.Errorf("create request is canceled or timed out"))
		s.Log.WithField("latency", time.Since(s.ServiceStart)).WithError(essential).Info("Failed to get network configuration, stopping service...")
		return
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
		nonEssential = taskmap.Write(s.CachedTask.TaskId, s.CachedTask, 0)
		if nonEssential != nil {
			s.Log.WithError(nonEssential).Warn("Failed to cache task, restarting...")
			return
		}

		s.Log.WithField("latency", time.Since(s.ServiceStart)).Info("network environment set")

		reply = make(map[string]interface{}, 0)
		reply["taskId"] = s.Env.UUID
		return reply, nil, nil
	}
}

func (s *startBasis) startDriverPhase(ctx context.Context) (reply map[string]interface{}, essential *utils.SeleniumError, nonEssential error) {
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
	case <-s.Request.Context().Done():
		essential = utils.CreationErr(fmt.Errorf("create request is canceled or timed out"))
		s.Log.WithField("latency", time.Since(s.ServiceStart)).WithError(essential).Info("Failed to start driver, stopping service...")
		return
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
	case reply = <-waitRequest.ResponseCh:
		var sessionId string
		sessionId, nonEssential = getSessionId(reply)
		if sessionId == "" {
			if nonEssential == nil {
				nonEssential = fmt.Errorf("session id in driver response is empty")
			}
			s.Log.WithError(err).Error("Failed to get sessionId")
			return
		}

		s.Log = s.Log.WithField(config.SessionIdKey, sessionId)

		var sess *sessionmap.Session
		sess, nonEssential = sessionmap.CreateEntity(sessionId, s.Env, s.CachedTask)
		if err != nil {
			s.Log.WithError(err).Error("Failed to cache driver session")
			return
		}
		// add session to ctx, so we can add it to selenium err log if any failure will happen later
		s.GinCtx.Set(config.SessionIdKey, sess)

		s.Log.WithField("latency", time.Since(s.ServiceStart)).Info("driver started")
		return reply, nil, nil
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
	basis    *startBasis
	finalize func(basis *startBasis)
}

func (starter genericStarter) StartService() (map[string]interface{}, *utils.SeleniumError) {
	//override request context, as after response is sent, request context is canceled
	starter.basis.Request = starter.basis.Request.WithContext(context.Background())
	go func() {
		_, startErr := basicStarter(starter).StartService()

		// abort launch if service startup returned error
		if startErr != nil {
			cachedTask, err := taskmap.FindByUuid(starter.basis.Env.UUID)
			if err == nil && cachedTask != nil {
				result, err := DescribeTask(cachedTask.TaskId)
				if err != nil {
					starter.basis.Log.WithError(err).Warn("failed to abort launch")
				} else {
					zebrunner.AbortTask(cachedTask, result.Tasks[0], startErr.Error())
				}
			}
		}
	}()

	return gin.H{"taskId": starter.basis.Env.UUID}, nil
}

type basicStarter struct {
	basis    *startBasis
	finalize func(basis *startBasis)
}

func (starter basicStarter) StartService() (map[string]interface{}, *utils.SeleniumError) {
	ctx, ctxCancel := context.WithTimeout(context.Background(), config.Conf.ServiceStartupTimeout)
	defer ctxCancel()

	starter.basis.ServiceStart = time.Now()
	starter.basis.Log.Info("service starting")

	for i := 0; true; i++ {
		logCopy := *starter.basis.Log
		starter.basis.Log = starter.basis.Log.WithField("attempt", i)
		for j, p := range starter.basis.Phases {
			reply, essential, nonEssential := p(ctx)
			if essential != nil {
				// stop service start, return error
				if starter.basis.CachedTask != nil {
					StopTask(starter.basis.CachedTask.TaskId, taskmap.TaskStartupFailure)
				}
				return nil, essential
			} else if nonEssential != nil {
				// flush data, next retry
				if starter.basis.CachedTask != nil {
					StopTask(starter.basis.CachedTask.TaskId, taskmap.TaskStartupFailure)
				}
				starter.basis.Log = &logCopy
				starter.basis.GinCtx.Set(config.TaskIdKey, "")
				starter.basis.GinCtx.Set(config.SessionIdKey, "")
				break
			} else if j == len(starter.basis.Phases)-1 {
				// last phase, no errors, finalize service start and return reply
				if starter.finalize != nil {
					starter.finalize(starter.basis)
				}
				starter.basis.Log.Info("service started")
				return reply, nil
			}
		}
	}

	return nil, utils.UnknownErr(fmt.Errorf("service startup failed"))
}

func GetServiceStarter(env *environment.ExecutionEnvironment, c *gin.Context, l *log.Entry) ServiceStarter {
	s := &startBasis{
		Log:     l,
		GinCtx:  c,
		Request: c.Request,
		Env:     env,
		Phases:  make([]phase, 0),
	}

	markAsGeneric := func(s *startBasis) {
		s.CachedTask.Status = taskmap.TaskGeneric
		taskmap.Write(s.CachedTask.TaskId, s.CachedTask, 0)
	}

	var starter ServiceStarter
	if strings.Contains(env.TaskDefinitionFamily, "generic") {
		s.appendPhase(s.registerTaskPhase).appendPhase(s.startTaskPhase)
		starter = genericStarter{basis: s, finalize: markAsGeneric}
	} else if strings.Contains(env.TaskDefinitionFamily, "cypress") {
		s.appendPhase(s.registerTaskPhase).appendPhase(s.startTaskPhase).appendPhase(s.setNetworkPhase)
		starter = basicStarter{basis: s, finalize: markAsGeneric}
	} else {
		s.appendPhase(s.registerTaskPhase).appendPhase(s.startTaskPhase).appendPhase(s.setNetworkPhase).appendPhase(s.startDriverPhase)
		starter = basicStarter{basis: s}
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

func getSessionId(resp map[string]interface{}) (string, error) {
	// Get sessionId from root. For unknown reason opera returns sessionId in root of object
	sessionId, ok := resp["sessionId"].(string)
	if ok {
		return sessionId, nil
	}

	// Get session from value
	value, ok := resp["value"].(map[string]interface{})
	if !ok {
		return "", fmt.Errorf("`value` must be an object")
	}

	sessionId, ok = value["sessionId"].(string)
	if ok {
		return sessionId, nil
	}

	return "", fmt.Errorf("failed to find sessionId field in response")
}
