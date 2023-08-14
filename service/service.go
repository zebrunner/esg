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
)

type ServiceStarter interface {
	StartService() (map[string]interface{}, *utils.SeleniumError)
}

type startBasis struct {
	ServiceStart time.Time
	Log          *log.Entry
	GinCtx       *gin.Context
	Env          *environment.ExecutionEnvironment
	Phases       []phase
	CachedTask   *taskmap.Task
	Task         *ecs.Task
}

// essential error -> stop service, non essential error -> retry service start, response chan -> successfull phase execution
type phase func(ctx context.Context) (map[string]interface{}, error, error)

func (s *startBasis) appendPhase(p phase) *startBasis {
	s.Phases = append(s.Phases, p)
	return s
}

func (s *startBasis) registerTaskPhase(ctx context.Context) (reply map[string]interface{}, essential error, nonEssential error) {
	s.Log.Debug("task registering")
	waitRequest := WaitForTaskRegister(ctx, *s.Env)
	select {
	case <-ctx.Done():
		s.Log.WithField("latency", time.Since(s.ServiceStart)).Info("Task register timed out")
		essential = ctx.Err()
		return
	case essential = <-waitRequest.EssentialErrCh:
		s.Log.WithField("latency", time.Since(s.ServiceStart)).WithError(essential).Info("Failed to register task, stopping service...")
		return
	case nonEssential = <-waitRequest.NonEssentialErrCh:
		s.Log.WithField("latency", time.Since(s.ServiceStart)).WithError(nonEssential).Warn("Failed to register task, restarting...")
		return
	case taskArn := <-waitRequest.ResponseCh:
		taskId := strings.Split(taskArn, "/")[2]
		s.Log = s.Log.WithField(config.TaskIdKey, taskId)

		// add arn to ctx, so we can add it to selenium err log if any failure will happen
		s.GinCtx.Set(config.TaskIdKey, taskId)

		s.CachedTask, nonEssential = taskmap.CreateEntity(taskId, s.Env)
		if nonEssential != nil {
			s.Log.WithError(nonEssential).Warn("Failed to cache task, restarting...")
			return
		}

		s.Log.WithField("latency", time.Since(s.ServiceStart)).Debug("task registered")

		reply = make(map[string]interface{}, 0)
		reply["taskId"] = s.CachedTask.ID
		return reply, nil, nil
	}
}

func (s *startBasis) startTaskPhase(ctx context.Context) (reply map[string]interface{}, essential error, nonEssential error) {
	s.Log.Info("task starting")
	waitRequest := taskWaiter.waitFor(ctx, s.CachedTask.ID)
	select {
	case <-ctx.Done():
		s.Log.WithField("latency", time.Since(s.ServiceStart)).Info("Task startup timed out")
		essential = ctx.Err()
		return
	case essential = <-waitRequest.EssentialErrCh:
		s.Log.WithField("latency", time.Since(s.ServiceStart)).WithError(essential).Info("Failed to start task, stopping service...")
		return
	case nonEssential = <-waitRequest.NonEssentialErrCh:
		s.Log.WithField("latency", time.Since(s.ServiceStart)).WithError(nonEssential).Warn("Failed to start task, restarting...")
		return
	case s.Task = <-waitRequest.ResponseCh:
		s.CachedTask.HealthAt = time.Now()
		s.CachedTask.Status = taskmap.TaskActive
		nonEssential = taskmap.Write(s.CachedTask.ID, s.CachedTask, 0)
		if nonEssential != nil {
			s.Log.WithError(nonEssential).Warn("Failed to cache task, restarting...")
			err := StopTask(s.CachedTask.ID, taskmap.TaskStartupFailure)
			if err != nil {
				s.Log.WithError(err).Warn("Failed to stop task")
			}
			return
		}

		s.Log.WithField("latency", time.Since(s.ServiceStart)).Info("task started")

		reply = make(map[string]interface{}, 0)
		reply["taskId"] = s.CachedTask.ID
		return reply, nil, nil
	}
}

func (s *startBasis) setNetworkPhase(ctx context.Context) (reply map[string]interface{}, essential error, nonEssential error) {
	s.Log.Debug("setting network environment")
	waitRequest := instanceWorker.waitForInstance(ctx, s.Task)
	select {
	case <-ctx.Done():
		s.Log.WithField("latency", time.Since(s.ServiceStart)).Info("Network configure timed out")
		essential = ctx.Err()
		return
	case essential = <-waitRequest.EssentialErrCh:
		s.Log.WithField("latency", time.Since(s.ServiceStart)).WithError(essential).Info("Failed to get network configuration, stopping service...")
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
			err := StopTask(s.CachedTask.ID, taskmap.TaskStartupFailure)
			if err != nil {
				s.Log.WithError(err).Warn("Failed to stop task")
			}
			return
		}

		s.CachedTask.Network = *s.Env.Network
		nonEssential = taskmap.Write(s.CachedTask.ID, s.CachedTask, 0)
		if nonEssential != nil {
			s.Log.WithError(nonEssential).Warn("Failed to cache task, restarting...")
			err := StopTask(s.CachedTask.ID, taskmap.TaskStartupFailure)
			if err != nil {
				s.Log.WithError(err).Warn("Failed to stop task")
			}
			return
		}

		s.Log.WithField("latency", time.Since(s.ServiceStart)).Info("task started")

		reply = make(map[string]interface{}, 0)
		reply["taskId"] = s.CachedTask.ID
		return reply, nil, nil
	}
}

func (s *startBasis) startDriverPhase(ctx context.Context) (reply map[string]interface{}, essential error, nonEssential error) {
	s.Log.Info("driver starting")

	u, ok := s.Env.Network.GetUrl("driver")
	if !ok {
		nonEssential = fmt.Errorf("failed to get driver network")
		s.Log.WithError(nonEssential).Warn("Failed to start driver, restarting...")
		err := StopTask(s.CachedTask.ID, taskmap.TaskStartupFailure)
		if err != nil {
			s.Log.WithError(err).Warn("Failed to stop task")
		}
		return
	}

	requestBody, err := s.Env.ReqCapabilities.ToRequestBody()
	if err != nil {
		essential = err
		s.Log.WithError(nonEssential).Warn("Failed to start driver, stopping service...")
		err := StopTask(s.CachedTask.ID, taskmap.TaskStartupFailure)
		if err != nil {
			s.Log.WithError(err).Warn("Failed to stop task")
		}
		return
	}

	reqUrl := &url.URL{}
	reqUrl.Host, reqUrl.Path = u.Host, path.Join(u.Path, s.GinCtx.Request.URL.Path)
	reqUrl.Scheme = "http"
	s.Log = s.Log.WithField("driver url", reqUrl)

	startSessionRequest, err := http.NewRequest(http.MethodPost, reqUrl.String(), requestBody)
	if err != nil {
		essential = err
		s.Log.WithError(nonEssential).Warn("Failed to start driver, stopping service...")
		err := StopTask(s.CachedTask.ID, taskmap.TaskStartupFailure)
		if err != nil {
			s.Log.WithError(err).Warn("Failed to stop task")
		}
		return
	}

	startSessionRequest.Header = s.GinCtx.Request.Header

	waitRequest := selenium.WaitForSessionStart(ctx, startSessionRequest)
	select {
	case <-ctx.Done():
		s.Log.WithField("latency", time.Since(s.ServiceStart)).Info("driver startup timed out")
		essential = ctx.Err()
		return
	case essential = <-waitRequest.EssentialErrCh:
		s.Log.WithField("latency", time.Since(s.ServiceStart)).WithError(essential).Info("Failed to start driver, stopping service...")
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
			err := StopTask(s.CachedTask.ID, taskmap.TaskStartupFailure)
			if err != nil {
				s.Log.WithError(err).Warn("Failed to stop task")
			}
			return
		}

		s.Log = s.Log.WithField(config.SessionIdKey, sessionId)

		_, nonEssential = sessionmap.CreateEntity(sessionId, s.Env, s.CachedTask)
		if err != nil {
			s.Log.WithError(err).Error("Failed to cache driver session")

			err := StopTask(s.CachedTask.ID, taskmap.TaskStartupFailure)
			if err != nil {
				s.Log.WithError(err).Warn("Failed to stop task")
			}
			return
		}

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

type starter struct {
	basis    *startBasis
	finalize func(basis *startBasis)
}

func (st starter) StartService() (map[string]interface{}, *utils.SeleniumError) {
	ctx, ctxCancel := context.WithTimeout(context.Background(), config.Conf.ServiceStartupTimeout)
	defer ctxCancel()

	st.basis.ServiceStart = time.Now()
	st.basis.Log.Info("service starting")
	for i := 0; true; i++ {
		logCopy := *st.basis.Log
		st.basis.Log = st.basis.Log.WithField("attempt", i)
		for j, p := range st.basis.Phases {
			reply, essential, nonEssential := p(ctx)

			if essential != nil {
				// stop service start, return error
				if essential == ctx.Err() {
					return nil, utils.CreationErr(fmt.Errorf("service startup timed out"))
				}
				return nil, utils.CreationErr(essential)
			} else if nonEssential != nil {
				// flush data, next retry
				st.basis.Log = &logCopy
				st.basis.CachedTask = nil
				st.basis.Task = nil
				break
			} else if j == len(st.basis.Phases)-1 {
				// last phase, no errors, finalize service start and return reply
				if st.finalize != nil {
					st.finalize(st.basis)
				}
				st.basis.Log.Info("service started")
				return reply, nil
			}
		}
	}

	return nil, utils.UnknownErr(fmt.Errorf("service startup failed"))
}

func GetServiceStarter(env *environment.ExecutionEnvironment, c *gin.Context, l *log.Entry) ServiceStarter {
	s := &startBasis{
		Log:    l,
		GinCtx: c,
		Env:    env,
		Phases: make([]phase, 0),
	}

	markAsGeneric := func(s *startBasis) {
		s.CachedTask.Status = taskmap.TaskGeneric
		taskmap.Write(s.CachedTask.ID, s.CachedTask, 0)
	}

	var st starter
	if strings.Contains(env.TaskDefinitionFamily, "generic") {
		s.appendPhase(s.registerTaskPhase)
		st = starter{
			basis:    s,
			finalize: markAsGeneric,
		}
	} else if strings.Contains(env.TaskDefinitionFamily, "cypress") {
		s.appendPhase(s.registerTaskPhase).appendPhase(s.startTaskPhase).appendPhase(s.setNetworkPhase)
		st = starter{
			basis:    s,
			finalize: markAsGeneric,
		}
	} else {
		s.appendPhase(s.registerTaskPhase).appendPhase(s.startTaskPhase).appendPhase(s.setNetworkPhase).appendPhase(s.startDriverPhase)
		st = starter{basis: s}
	}

	return st
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
