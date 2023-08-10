package service

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go/service/ec2"
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
	CachedTask   *taskmap.Task
	Task         *ecs.Task
}

// essential error -> stop service, non essential error -> retry service start, response chan -> successfull phase execution
type waitAdapter[Input, Output any] func(context.Context, Input) (<-chan error, <-chan error, <-chan Output)

type phase func(ctx context.Context) (map[string]interface{}, error, error)

var (
	registerTaskAdapter waitAdapter[environment.ExecutionEnvironment, string] = func(ctx context.Context, env environment.ExecutionEnvironment) (<-chan error, <-chan error, <-chan string) {
		waitRequest := WaitForTaskRegister(ctx, env)
		return waitRequest.EssentialErrCh, waitRequest.NonEssentialErrCh, waitRequest.ResponseChan
	}

	startTaskAdapter waitAdapter[string, *ecs.Task] = func(ctx context.Context, taskArn string) (<-chan error, <-chan error, <-chan *ecs.Task) {
		waitRequest := taskWaiter.waitFor(ctx, taskArn)
		return waitRequest.EssentialErrCh, waitRequest.NonEssentialErrCh, waitRequest.ResponseChan
	}

	findInstanceAdapter waitAdapter[*ecs.Task, *ec2.Instance] = func(ctx context.Context, task *ecs.Task) (<-chan error, <-chan error, <-chan *ec2.Instance) {
		waitRequest := instanceWorker.waitForInstance(ctx, task)
		return waitRequest.EssentialErrCh, waitRequest.NonEssentialErrCh, waitRequest.ResponseChan
	}

	startSessionAdapter waitAdapter[*http.Request, map[string]interface{}] = func(ctx context.Context, request *http.Request) (<-chan error, <-chan error, <-chan map[string]interface{}) {
		waitRequest := selenium.WaitForSessionStart(ctx, request)
		return waitRequest.EssentialErrCh, waitRequest.NonEssentialErrCh, waitRequest.ResponseChan
	}
)

func (s *startBasis) registerTaskPhase(ctx context.Context) (reply map[string]interface{}, essential error, nonEssential error) {
	s.Log.Debug("task registering")
	essentialErrCh, nonEsentialErrCh, responseCh := registerTaskAdapter(ctx, *s.Env)
	select {
	case <-ctx.Done():
		s.Log.WithField("latency", time.Since(s.ServiceStart)).Info("Task register timed out")
		essential = ctx.Err()
		return
	case essential = <-essentialErrCh:
		s.Log.WithField("latency", time.Since(s.ServiceStart)).WithError(essential).Info("Failed to register task, stopping service...")
		return
	case nonEssential = <-nonEsentialErrCh:
		s.Log.WithField("latency", time.Since(s.ServiceStart)).WithError(nonEssential).Warn("Failed to register task, restarting...")
		return
	case taskArn := <-responseCh:
		taskId := strings.Split(taskArn, "/")[2]
		s.Log = s.Log.WithField("_taskId", taskId)
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
	essentialErrCh, nonEsentialErrCh, responseCh := startTaskAdapter(ctx, s.CachedTask.ID)
	select {
	case <-ctx.Done():
		s.Log.WithField("latency", time.Since(s.ServiceStart)).Info("Task startup timed out")
		essential = ctx.Err()
		return
	case essential = <-essentialErrCh:
		s.Log.WithField("latency", time.Since(s.ServiceStart)).WithError(essential).Info("Failed to start task, stopping service...")
		return
	case nonEssential = <-nonEsentialErrCh:
		s.Log.WithField("latency", time.Since(s.ServiceStart)).WithError(nonEssential).Warn("Failed to start task, restarting...")
		return
	case s.Task = <-responseCh:
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
	essentialErrCh, nonEsentialErrCh, responseCh := findInstanceAdapter(ctx, s.Task)
	select {
	case <-ctx.Done():
		s.Log.WithField("latency", time.Since(s.ServiceStart)).Info("Network configure timed out")
		essential = ctx.Err()
		return
	case essential = <-essentialErrCh:
		s.Log.WithField("latency", time.Since(s.ServiceStart)).WithError(essential).Info("Failed to get network configuration, stopping service...")
		return
	case nonEssential = <-nonEsentialErrCh:
		s.Log.WithField("latency", time.Since(s.ServiceStart)).WithError(nonEssential).Warn("Failed to get Network configuration, restarting...")
		return
	case instance := <-responseCh:
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
		nonEssential = errors.New("failed to get driver network")
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

	essentialErrCh, nonEsentialErrCh, responseCh := startSessionAdapter(ctx, startSessionRequest)
	select {
	case <-ctx.Done():
		s.Log.WithField("latency", time.Since(s.ServiceStart)).Info("driver startup timed out")
		essential = ctx.Err()
		return
	case essential = <-essentialErrCh:
		s.Log.WithField("latency", time.Since(s.ServiceStart)).WithError(essential).Info("Failed to start driver, stopping service...")
		return
	case nonEssential = <-nonEsentialErrCh:
		s.Log.WithField("latency", time.Since(s.ServiceStart)).WithError(nonEssential).Warn("Failed to start driver, restarting...")
		return
	case reply = <-responseCh:
		var sessionId string
		sessionId, nonEssential = getSessionId(reply)
		if sessionId == "" {
			if nonEssential == nil {
				nonEssential = errors.New("session id in driver response is empty")
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
	basis    startBasis
	phases   []phase
	finalize func(basis startBasis)
}

func (st *starter) appendPhase(p phase) *starter {
	st.phases = append(st.phases, p)
	return st
}

func (st starter) StartService() (map[string]interface{}, *utils.SeleniumError) {
	ctx, ctxCancel := context.WithTimeout(context.Background(), config.Conf.ServiceStartupTimeout)
	defer ctxCancel()

	st.basis.ServiceStart = time.Now()
	st.basis.Log.Info("service starting")
	for i := 0; true; i++ {
		logCopy := *st.basis.Log
		st.basis.Log = st.basis.Log.WithField("attempt", i)
		for j, p := range st.phases {
			st.basis.Log.Info("phase ", i)
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
			} else if j == len(st.phases)-1 {
				// last phase, no errors, finalize start and return reply
				if st.finalize != nil {
					st.finalize(st.basis)
				}
				st.basis.Log.Info("service started")
				return reply, nil
			}
		}
	}

	return nil, nil
}

func GetServiceStarter(env *environment.ExecutionEnvironment, c *gin.Context, l *log.Entry) ServiceStarter {
	st := starter{
		basis: startBasis{
			Log:    l,
			GinCtx: c,
			Env:    env,
		},
		phases: make([]phase, 0),
	}

	markAsGeneric := func(s startBasis) {
		s.CachedTask.Status = taskmap.TaskGeneric
		taskmap.Write(s.CachedTask.ID, s.CachedTask, 0)
	}

	if strings.Contains(env.TaskDefinitionFamily, "generic") {
		st.appendPhase(st.basis.registerTaskPhase)
		st.finalize = markAsGeneric
	} else if strings.Contains(env.TaskDefinitionFamily, "cypress") {
		st.appendPhase(st.basis.registerTaskPhase).appendPhase(st.basis.startTaskPhase).appendPhase(st.basis.setNetworkPhase)
		st.finalize = markAsGeneric
	} else {
		st.appendPhase(st.basis.registerTaskPhase).appendPhase(st.basis.startTaskPhase).appendPhase(st.basis.setNetworkPhase).appendPhase(st.basis.startDriverPhase)
	}
	log.Debug(*st.basis.Log, st.basis.GinCtx, *st.basis.Env)
	log.Debug("phases??: ", st.phases)
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
		return "", errors.New("`value` must be an object")
	}

	sessionId, ok = value["sessionId"].(string)
	if ok {
		return sessionId, nil
	}

	return "", errors.New("failed to find sessionId field in response")
}
