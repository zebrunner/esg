package service

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go/service/ecs"
	"github.com/gin-gonic/gin"
	log "github.com/sirupsen/logrus"
	"github.com/zebrunner/esg/cachemaps/mapper"
	"github.com/zebrunner/esg/cachemaps/utilsmap"
	"github.com/zebrunner/esg/config"
	"github.com/zebrunner/esg/environment"
	"github.com/zebrunner/esg/selenium"
	"github.com/zebrunner/esg/utils"
	"github.com/zebrunner/esg/zebrunner"
)

var (
	GenericCtxWorker CtxWorker
)

func init() {
	GenericCtxWorker = CtxWorker{
		ctxMutex: sync.Mutex{},
		CtxMap:   make(map[string]context.Context, 0),
	}
}

type CtxWorker struct {
	ctxMutex sync.Mutex
	CtxMap   map[string]context.Context
}

func (ctxWorker *CtxWorker) append(routerUUID string, ctx context.Context) {
	ctxWorker.ctxMutex.Lock()
	ctxWorker.CtxMap[routerUUID] = ctx
	ctxWorker.ctxMutex.Unlock()
	go func(genericUUID string, ctx context.Context) {
		<-ctx.Done()
		ctxWorker.ctxMutex.Lock()
		delete(ctxWorker.CtxMap, genericUUID)
		ctxWorker.ctxMutex.Unlock()
	}(routerUUID, ctx)
}

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
	MapperEntity *mapper.Mapper
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
				StopTaskForcibly(taskId, mapper.TaskStartupFailure)
				return
			}
		}()
		return
	case essentialReason := <-waitRequest.EssentialErrCh:
		s.Log.WithField("latency", time.Since(s.ServiceStart)).WithError(essentialReason).Info("Failed to register task, stopping service...")
		essential = utils.CreationErr(fmt.Errorf("failed to create task"), essentialReason.Error())
		return
	case nonEssential = <-waitRequest.NonEssentialErrCh:
		s.Log.WithField("latency", time.Since(s.ServiceStart)).WithError(nonEssential).Warn("Failed to register task, restarting...")
		return
	case taskArn := <-waitRequest.ResponseCh:
		taskId := strings.Split(taskArn, "/")[2]

		s.Log = s.Log.WithField(config.TaskIdKey, taskId)
		s.TaskId = &taskId
		s.MapperEntity.TaskId = taskId

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
		s.MapperEntity.HealthAt = &healthyTime
		s.MapperEntity.AccessedAt = &healthyTime

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
		s.Log.WithField("latency", time.Since(s.ServiceStart)).WithError(essentialReason).Info("Failed to get network configuration, stopping service...")
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

		s.MapperEntity.Network = *s.Env.Network

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
		essential = utils.CreationErr(fmt.Errorf("failed to get request body for driver start"), err.Error())
		s.Log.WithError(essential).Warn("Failed to start driver, stopping service...")
		return
	}

	reqUrl := &url.URL{}
	reqUrl.Host, reqUrl.Path = u.Host, path.Join(u.Path, s.Request.URL.Path)
	reqUrl.Scheme = "http"
	s.Log = s.Log.WithField("driver url", reqUrl)

	startSessionRequest, err := http.NewRequest(http.MethodPost, reqUrl.String(), requestBody)
	if err != nil {
		essential = utils.CreationErr(fmt.Errorf("failed to create start driver request"), err.Error())
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

		nonEssential = modifyDriverReply(s.Reply, s.Env)
		if nonEssential != nil {
			s.Log.WithError(nonEssential).Error("Failed to modify driver reply")
			return
		}
		s.MapperEntity.SessionID = sessionId

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
	genericCtx, genericCtxCancel := context.WithCancel(context.Background())
	// add to genericCtxMap
	GenericCtxWorker.append(starter.basis.Env.RouterUUID, genericCtx)
	// override request context, as after response is sent, request context is canceled
	starter.basis.Request = starter.basis.Request.WithContext(genericCtx)
	go func() {
		// create new task definition for generic task
		taskDefinition, err := CreateTaskDefinition(starter.basis.Env)
		// abort launch if failed to create new task definition
		if err != nil {
			log.WithError(err).Error("Failed to create task definition")
			zebrunner.AbortLaunch(starter.basis.Env.RouterUUID, starter.basis.Env.Workspace,
				starter.basis.Env.Capabilities.LaunchUUID.ToPrimitive(), fmt.Sprintf("failed to create task defenition for generic: %v", err.Error()))

			genericCtxCancel()
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
		// stop generic context
		genericCtxCancel()
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

	for i := 0; true; i++ {
		logCopy := *starter.basis.Log
		mapperEntityCopy := *starter.basis.MapperEntity
		starter.basis.Log = starter.basis.Log.WithField("attempt", i)
		success := true

		for _, phase := range starter.basis.Phases {
			essential, nonEssential := phase(startupTime)
			if starter.basis.Request.Context().Err() != nil {
				essential = utils.CreationErr(fmt.Errorf("service start has been canceled"))
			}

			if essential != nil {
				// stop service starter, return error
				if starter.basis.MapperEntity != nil {
					StopTask(*starter.basis.MapperEntity, mapper.TaskStartupFailure)
					starter.basis.GinCtx.Set(config.RouterUUID, starter.basis.MapperEntity)
				}
				return nil, essential
			}

			if nonEssential != nil {
				// flush data, next retry
				if starter.basis.MapperEntity != nil {
					// check abort status in case of non esential error
					task, err := mapper.Find(starter.basis.MapperEntity.TaskId, false)
					if err == nil && (task.StopReason == mapper.TaskAborted || task.StopReason == mapper.TaskFinished) {
						// stop service starter, return error
						return nil, utils.CreationErr(fmt.Errorf(string(task.StopReason)))
					}
					StopTaskForcibly(starter.basis.MapperEntity.TaskId, mapper.TaskStartupFailure)
				}
				starter.basis.Log = &logCopy
				starter.basis.MapperEntity = &mapperEntityCopy
				starter.basis.Task = nil
				starter.basis.TaskId = nil
				// flag for retries execution
				success = false
				break
			}
		}

		if success {
			// all phases executed, no errors, finalize service start and return reply
			starter.basis.GinCtx.Set(config.RouterUUID, starter.basis.MapperEntity)
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

	var err error
	basis.MapperEntity, err = mapper.CreateEntity(env, 15*time.Minute)
	if err != nil {
		basis.Log.WithError(err).Error("Failed to create mapper entity")
	}
	basis.GinCtx.Set(config.RouterUUID, basis.MapperEntity)

	var starter ServiceStarter
	if strings.Contains(env.TaskDefinitionFamily, "generic") {
		basis.appendPhase(basis.registerTaskPhase).appendPhase(basis.startTaskPhase)

		starter = genericStarter{
			basis: basis,
			finalizeFunc: func(s *startBasis) {
				// #1056: Small chance of double shaping on generic task abort
				for {
					if ok := utilsmap.AcquireLock(s.MapperEntity.RouterUUID, 0); ok {
						break
					}
					time.Sleep(10 * time.Second)
				}

				mapperEntity, err := mapper.Find(s.MapperEntity.RouterUUID, false)
				if err == nil && mapperEntity != nil {
					tmpMapperEntity := s.MapperEntity
					s.MapperEntity = mapperEntity
					s.MapperEntity.HealthAt = tmpMapperEntity.HealthAt
					s.MapperEntity.TaskId = tmpMapperEntity.TaskId
				} else {
					s.MapperEntity.Status = mapper.Active
				}

				err = mapper.Write(s.MapperEntity, 0)
				if err != nil {
					s.Log.WithError(err).Error("Failed to recache task on finalize!")
				}

				err = mapper.AppendToSet(mapper.TASK, s.MapperEntity.RouterUUID)
				if err != nil {
					s.Log.WithError(err).Error("Failed to append to task set on finalize!")
				}

				err = utilsmap.ReleaseLock(s.MapperEntity.TaskId)
				if err != nil {
					s.Log.WithError(err).Error("Failed to release lock on finalize!")
				}
			},
		}
	} else if strings.Contains(env.TaskDefinitionFamily, "cypress") {
		basis.appendPhase(basis.registerTaskPhase).appendPhase(basis.startTaskPhase).appendPhase(basis.setNetworkPhase)

		starter = basicStarter{
			basis: basis,
			finalizeFunc: func(s *startBasis) {
				accessedAt := time.Now()
				s.MapperEntity.AccessedAt = &accessedAt
				s.MapperEntity.Status = mapper.Cypress

				err := mapper.WritedByWorker(s.MapperEntity, []mapper.SetType{mapper.TASK}, nil, 0)
				if err != nil {
					basis.Log.WithError(err).Error("Failed to recache mapper on finalize!")
				}
			},
		}
	} else {
		basis.appendPhase(basis.registerTaskPhase).appendPhase(basis.startTaskPhase).appendPhase(basis.setNetworkPhase).appendPhase(basis.startDriverPhase)
		starter = basicStarter{
			basis: basis,
			finalizeFunc: func(s *startBasis) {
				accessedAt := time.Now()
				s.MapperEntity.AccessedAt = &accessedAt
				s.MapperEntity.Status = mapper.Active

				err := mapper.WritedByWorker(s.MapperEntity, []mapper.SetType{mapper.SESSION, mapper.TASK}, nil, 0)
				if err != nil {
					basis.Log.WithError(err).Error("Failed to recache task on finalize!")
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

func modifyDriverReply(driverReply map[string]interface{}, env *environment.ExecutionEnvironment) error {
	capsToAdd := map[string]interface{}{
		"enableVideo": env.Capabilities.EnableVideo,
		"enableVNC":   env.Capabilities.EnableVNC,
	}

	value, ok := driverReply["value"].(map[string]interface{})
	if !ok {
		return fmt.Errorf("`value` must be an object")
	}

	capabilities, ok := value["capabilities"].(map[string]interface{})
	if !ok {
		return fmt.Errorf("`capabilities` must be an object")
	}

	for k, v := range capsToAdd {
		capabilities[k] = v
	}

	return nil
}
