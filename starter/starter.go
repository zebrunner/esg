package starter

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go/service/ecs"
	"github.com/gin-gonic/gin"
	log "github.com/sirupsen/logrus"
	"github.com/zebrunner/esg/cachemaps"
	"github.com/zebrunner/esg/cachemaps/mapper"
	"github.com/zebrunner/esg/cachemaps/utilsmap"
	"github.com/zebrunner/esg/capabilities"
	"github.com/zebrunner/esg/config"
	"github.com/zebrunner/esg/environment"
	envtype "github.com/zebrunner/esg/environment/envType"
	"github.com/zebrunner/esg/selenium"
	"github.com/zebrunner/esg/service"
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
	ServiceStart  time.Time
	Log           *log.Entry
	GinCtx        *gin.Context
	Request       *http.Request
	DriverReqCaps *capabilities.RequestCaps
	Env           *environment.ExecutionEnvironment
	Phases        []phase
	Task          *ecs.Task
	TaskId        *string
	MapperEntity  *mapper.Mapper
	Reply         map[string]interface{}
}

// essential error -> stop service, non essential error -> retry service start
type phase func(ctx context.Context) (*utils.SeleniumError, error)

func (s *startBasis) appendPhase(p phase) *startBasis {
	s.Phases = append(s.Phases, p)
	return s
}

func (s *startBasis) registerTaskPhase(ctx context.Context) (essential *utils.SeleniumError, nonEssential error) {
	s.Log.Debug("task registering")
	waitRequest := WaitForTaskRegister(ctx, s.Env, s.MapperEntity.RouterUUID)
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
				service.StopTaskForcibly(taskId, mapper.TaskStartupFailure)
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
		s.MapperEntity.TaskId = taskId

		s.Log.WithField("latency", time.Since(s.ServiceStart)).Debug("task registered")
		s.Reply = map[string]interface{}{"taskId": s.MapperEntity.RouterUUID}
		return nil, nil
	}
}

func (s *startBasis) startTaskPhase(ctx context.Context) (essential *utils.SeleniumError, nonEssential error) {
	s.Log.Info("task starting")
	waitRequest := taskWaiter.waitFor(ctx, s.MapperEntity.TaskId)
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
		s.Reply = map[string]interface{}{"taskId": s.MapperEntity.RouterUUID}
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
		s.Log.WithFields(log.Fields{
			"instanceId": instance.ImageId,
			"taskArn":    (s.Task.TaskArn),
			"latency":    time.Since(s.ServiceStart),
		}).Debug("Instance is ready, starting network configuration")

		// Enhanced AWS VPC network setup with robust IP waiting and validation
		ip, err := s.waitForPrivateIPWithRetry(ctx)
		if err != nil {
			s.Log.WithError(err).Error("Failed to acquire private IP address")
			nonEssential = err
			return
		}

		s.Env.Network.IP = ip
		s.Log.WithFields(log.Fields{
			"taskArn":    (s.Task.TaskArn),
			"taskId":     (s.TaskId),
			"privateIP":  ip,
			"eniDetails": s.Task.Attachments,
		}).Info("Task ENI private IP acquired successfully")

		// Validate network readiness before proceeding
		if err := s.validateNetworkReadiness(ctx); err != nil {
			s.Log.WithError(err).Error("Network readiness validation failed")
			nonEssential = err
			return
		}

		s.MapperEntity.Network = *s.Env.Network

		s.Log.WithFields(log.Fields{
			"privateIP": ip,
			"latency":   time.Since(s.ServiceStart),
		}).Info("Network environment set successfully")
		s.Reply = map[string]interface{}{"taskId": s.MapperEntity.RouterUUID}
		return nil, nil
	}
}

func (s *startBasis) startDriverPhase(ctx context.Context) (essential *utils.SeleniumError, nonEssential error) {
	s.Log.Info("driver starting")

	// Enhanced driver startup with pre-validation
	s.Log.WithFields(log.Fields{
		"privateIP": s.Env.Network.IP,
		"caps":      s.DriverReqCaps,
		"latency":   time.Since(s.ServiceStart),
	}).Debug("Starting driver with network configuration")

	waitRequest := selenium.WaitForSessionStart(ctx, s.Env.Network, s.DriverReqCaps)
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
		s.Log.WithFields(log.Fields{
			"reply":   s.Reply,
			"latency": time.Since(s.ServiceStart),
		}).Debug("Driver session response received")

		var sessionId string
		// replace sessionId from driver response with router uuid
		sessionId, nonEssential = replaceSessionId(s.Reply, s.MapperEntity.RouterUUID)
		if sessionId == "" {
			if nonEssential == nil {
				nonEssential = fmt.Errorf("session id in driver response is empty")
			}
			s.Log.WithError(nonEssential).Error("Failed to get sessionId")
			return
		}

		s.Log = s.Log.WithField(config.SessionIdKey, sessionId)

		nonEssential = modifyDriverReply(s.Reply, s.Env)
		if nonEssential != nil {
			s.Log.WithError(nonEssential).Error("Failed to modify driver reply")
			return
		}
		s.MapperEntity.SessionID = sessionId

		// Enhanced logging with session validation
		s.Log.WithFields(log.Fields{
			"taskArn":    (s.Task.TaskArn),
			"taskId":     (s.TaskId),
			"privateIP":  s.Env.Network.IP,
			"sessionId":  sessionId,
			"routerUUID": s.MapperEntity.RouterUUID,
			"caps":       s.DriverReqCaps,
			"latency":    time.Since(s.ServiceStart),
		}).Info("Driver session created successfully")

		s.Log.WithField("latency", time.Since(s.ServiceStart)).Info("driver started and validated")
		return nil, nil
	}
}

type genericStarter struct {
	basis        *startBasis
	finalizeFunc func(basis *startBasis)
}

func (starter genericStarter) StartService(startupTime context.Context) (map[string]interface{}, *utils.SeleniumError) {
	genericCtx, genericCtxCancel := context.WithCancel(context.Background())
	// add to genericCtxMap
	GenericCtxWorker.append(starter.basis.MapperEntity.RouterUUID, genericCtx)
	// override request context, as after response is sent, request context is canceled
	starter.basis.Request = starter.basis.Request.WithContext(genericCtx)
	go func() {
		// create new task definition for generic task
		taskDefinition, err := service.CreateTaskDefinition(starter.basis.Env.ContainerDefinitions(), starter.basis.Env.Volume(), starter.basis.Env.TaskDefinitionFamily, starter.basis.Env.TaskRoleArn)
		// abort launch if failed to create new task definition
		if err != nil {
			log.WithError(err).Error("Failed to create task definition")
			zebrunner.AbortLaunch(starter.basis.MapperEntity.RouterUUID, starter.basis.MapperEntity.Workspace,
				starter.basis.Env.Capabilities.LaunchUUID.ToPrimitive(), fmt.Sprintf("failed to create task defenition for generic: %v", err.Error()))

			genericCtxCancel()
			return
		}
		// set revision of newly created task definition
		starter.basis.Env.TaskDefinitionFamily = fmt.Sprintf("%s:%v", starter.basis.Env.TaskDefinitionFamily, *taskDefinition.Revision)

		_, startErr := basicStarter(starter).StartService(startupTime)

		// abort launch if service startup returned error
		if startErr != nil {
			zebrunner.AbortLaunch(starter.basis.MapperEntity.RouterUUID, starter.basis.MapperEntity.Workspace,
				starter.basis.Env.Capabilities.LaunchUUID.ToPrimitive(), startErr.Error())
		}
		// stop generic context
		genericCtxCancel()
	}()

	return gin.H{"taskId": starter.basis.MapperEntity.RouterUUID}, nil
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

			me, _ := mapper.Find(starter.basis.MapperEntity.RouterUUID)
			var exitError *utils.SeleniumError
			if essential != nil {
				starter.basis.MapperEntity.StopReason = mapper.TaskStartupFailure
				exitError = essential
			} else if me != nil && (me.StopReason == mapper.TaskAborted || me.StopReason == mapper.TaskFinished) {
				starter.basis.MapperEntity.StopReason = me.StopReason
				exitError = utils.CreationErr(fmt.Errorf("%s", me.StopReason))
			} else if starter.basis.Request.Context().Err() != nil {
				starter.basis.MapperEntity.StopReason = mapper.TaskStartupFailure
				exitError = utils.CreationErr(fmt.Errorf("service start has been canceled"))
			}

			if exitError != nil {
				starter.basis.GinCtx.Set(config.RouterUUID, starter.basis.MapperEntity)
				starter.finalizeOnFailure()
				return nil, exitError
			}

			if nonEssential != nil {
				// flush data, next retry
				service.StopTaskForcibly(starter.basis.MapperEntity.TaskId, mapper.TaskStartupFailure)
				starter.basis.Log = &logCopy
				starter.basis.MapperEntity = &mapperEntityCopy
				starter.basis.Task = nil
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

func (starter basicStarter) finalizeOnFailure() {
	// stop service starter, return error
	starter.basis.MapperEntity.Status = mapper.Stopped
	if starter.basis.MapperEntity.TaskId == "" {
		err := mapper.WritedByWorker(starter.basis.MapperEntity, nil, nil, 5*time.Minute)
		if err != nil {
			starter.basis.Log.WithError(err).Error("Failed to update task's cache!")
		}

	} else {
		err := service.StopTaskForcibly(starter.basis.MapperEntity.TaskId, starter.basis.MapperEntity.StopReason)
		if err != nil {
			starter.basis.Log.WithError(err).Error("Failed to stop task on failure")
		}

		err = mapper.WritedByWorker(starter.basis.MapperEntity, []cachemaps.SetType{cachemaps.TASK}, nil, 0)
		if err != nil {
			starter.basis.Log.WithError(err).Error("Failed to update task's cache!")
		}
	}
}

func GetServiceStarter(env *environment.ExecutionEnvironment, workspace string, routerUUID string, reqCaps *capabilities.RequestCaps, c *gin.Context, l *log.Entry) ServiceStarter {
	basis := &startBasis{
		Log:           l,
		GinCtx:        c,
		Request:       c.Request,
		Env:           env,
		Phases:        make([]phase, 0),
		Reply:         make(map[string]interface{}, 0),
		DriverReqCaps: reqCaps,
	}

	var err error
	basis.MapperEntity, err = mapper.CreateEntity(workspace, routerUUID, env.Capabilities, env.Network, 15*time.Minute)
	if err != nil {
		basis.Log.WithError(err).Error("Failed to create mapper entity")
	}
	basis.GinCtx.Set(config.RouterUUID, basis.MapperEntity)

	var starter ServiceStarter
	if env.Type == envtype.GENERIC {
		basis.appendPhase(basis.registerTaskPhase).appendPhase(basis.startTaskPhase)

		starter = genericStarter{
			basis: basis,
			finalizeFunc: func(s *startBasis) {
				// #1056: Small chance of double shaping on generic task abort
				for {
					if ok := utilsmap.AcquireLock(s.MapperEntity.RouterUUID); ok {
						break
					}
					time.Sleep(10 * time.Second)
				}

				actuallMapperEntity, _ := mapper.Find(s.MapperEntity.RouterUUID)
				if actuallMapperEntity == nil || actuallMapperEntity.StopReason == "" {
					s.MapperEntity.Status = mapper.Active
				} else if actuallMapperEntity.Status == mapper.Stopped {
					s.MapperEntity.Status = actuallMapperEntity.Status
					s.MapperEntity.StopReason = actuallMapperEntity.StopReason
				} else {
					s.MapperEntity.StopReason = actuallMapperEntity.StopReason
					s.MapperEntity.Status = mapper.Stopped

					err := service.StopTaskForcibly(s.MapperEntity.TaskId, actuallMapperEntity.StopReason)
					if err != nil {
						s.Log.WithError(err).Error("Failed to stop generic task on finalize!")
					}
				}

				err := mapper.WritedByWorker(s.MapperEntity, []cachemaps.SetType{cachemaps.TASK}, nil, 0)
				if err != nil {
					basis.Log.WithError(err).Error("Failed to recache task on finalize!")
				}

				err = utilsmap.ReleaseLock(s.MapperEntity.RouterUUID)
				if err != nil {
					s.Log.WithError(err).Error("Failed to release lock on finalize!")
				}
			},
		}
	} else if env.Type == envtype.CYPRESS {
		basis.appendPhase(basis.registerTaskPhase).appendPhase(basis.startTaskPhase).appendPhase(basis.setNetworkPhase)

		starter = basicStarter{
			basis: basis,
			finalizeFunc: func(s *startBasis) {
				accessedAt := time.Now()
				s.MapperEntity.AccessedAt = &accessedAt
				s.MapperEntity.Status = mapper.Cypress

				err := mapper.WritedByWorker(s.MapperEntity, []cachemaps.SetType{cachemaps.TASK}, nil, 0)
				if err != nil {
					basis.Log.WithError(err).Error("Failed to recache task on finalize!")
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

				err := mapper.WritedByWorker(s.MapperEntity, []cachemaps.SetType{cachemaps.SESSION, cachemaps.TASK}, nil, 0)
				if err != nil {
					basis.Log.WithError(err).Error("Failed to recache task on finalize!")
				}
			},
		}
	}

	return starter
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

// waitForPrivateIPWithRetry implements robust IP waiting logic for AWS VPC mode
// It handles the race condition where ENI attachment might be delayed
func (s *startBasis) waitForPrivateIPWithRetry(ctx context.Context) (string, error) {
	const maxRetries = 10
	const retryInterval = 2 * time.Second

	s.Log.Debug("Starting robust private IP acquisition for AWS VPC")

	for attempt := 0; attempt < maxRetries; attempt++ {
		select {
		case <-ctx.Done():
			return "", fmt.Errorf("context cancelled while waiting for private IP")
		default:
		}

		// Try to get the private IP from task attachments
		ip := utils.GetAwsVpcTaskPrivateIPv4(s.Task.Attachments)

		if ip != "" {
			s.Log.WithFields(log.Fields{
				"attempt":   attempt + 1,
				"privateIP": ip,
				"latency":   time.Since(s.ServiceStart),
			}).Debug("Private IP acquired successfully")
			return ip, nil
		}

		// Log the attempt with detailed information
		s.Log.WithFields(log.Fields{
			"attempt":     attempt + 1,
			"maxRetries":  maxRetries,
			"retryIn":     retryInterval,
			"attachments": len(s.Task.Attachments),
		}).Warn("Private IP not found in task attachments, retrying...")

		// Log attachment details for debugging
		for i, attachment := range s.Task.Attachments {
			if attachment != nil {
				s.Log.WithFields(log.Fields{
					"attachmentIndex":  i,
					"attachmentId":     attachment.Id,
					"attachmentType":   attachment.Type,
					"attachmentStatus": attachment.Status,
					"detailsCount":     len(attachment.Details),
				}).Debug("Attachment details")

				// Log each detail for debugging
				for j, detail := range attachment.Details {
					if detail != nil && detail.Name != nil && detail.Value != nil {
						s.Log.WithFields(log.Fields{
							"attachmentIndex": i,
							"detailIndex":     j,
							"detailName":      *detail.Name,
							"detailValue":     *detail.Value,
						}).Debug("Attachment detail")
					}
				}
			}
		}

		// Wait before retrying, but respect context cancellation
		select {
		case <-ctx.Done():
			return "", fmt.Errorf("context cancelled while waiting for private IP")
		case <-time.After(retryInterval):
			// Continue to next attempt
		}
	}

	return "", fmt.Errorf("failed to acquire private IP after %d attempts", maxRetries)
}

// validateNetworkReadiness performs comprehensive network connectivity validation
func (s *startBasis) validateNetworkReadiness(ctx context.Context) error {
	s.Log.Debug("Starting network readiness validation")

	// Test 1: Validate network URL generation
	url, ok := s.Env.Network.GetUrl("driver")
	if !ok {
		return fmt.Errorf("failed to generate driver URL from network configuration")
	}

	s.Log.WithFields(log.Fields{
		"driverURL": url.String(),
		"host":      url.Host,
		"scheme":    url.Scheme,
		"path":      url.Path,
	}).Debug("Driver URL generated successfully")

	// Test 2: Validate TCP connectivity to driver port
	if err := s.validateTCPConnectivity(ctx, url.Host); err != nil {
		return fmt.Errorf("TCP connectivity validation failed: %v", err)
	}

	// Test 3: Validate HTTP connectivity to driver endpoint
	if err := s.validateHTTPConnectivity(ctx, url.String()); err != nil {
		return fmt.Errorf("HTTP connectivity validation failed: %v", err)
	}

	s.Log.WithFields(log.Fields{
		"driverURL": url.String(),
		"latency":   time.Since(s.ServiceStart),
	}).Info("Network readiness validation completed successfully")

	return nil
}

// validateTCPConnectivity tests basic TCP connectivity to the driver port
func (s *startBasis) validateTCPConnectivity(ctx context.Context, host string) error {
	const maxRetries = 5
	const retryInterval = 1 * time.Second

	s.Log.WithField("host", host).Debug("Starting TCP connectivity validation")

	for attempt := 0; attempt < maxRetries; attempt++ {
		select {
		case <-ctx.Done():
			return fmt.Errorf("context cancelled during TCP connectivity test")
		default:
		}

		// Create a timeout context for the connection attempt
		connCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()

		// Attempt to establish TCP connection
		conn, err := s.dialWithContext(connCtx, "tcp", host)
		if err == nil {
			conn.Close()
			s.Log.WithFields(log.Fields{
				"host":    host,
				"attempt": attempt + 1,
			}).Debug("TCP connectivity validation successful")
			return nil
		}

		s.Log.WithFields(log.Fields{
			"host":    host,
			"attempt": attempt + 1,
			"error":   err.Error(),
		}).Warn("TCP connectivity test failed, retrying...")

		// Wait before retrying
		select {
		case <-ctx.Done():
			return fmt.Errorf("context cancelled during TCP connectivity test")
		case <-time.After(retryInterval):
			// Continue to next attempt
		}
	}

	return fmt.Errorf("TCP connectivity validation failed after %d attempts", maxRetries)
}

// validateHTTPConnectivity tests HTTP connectivity to the driver endpoint
func (s *startBasis) validateHTTPConnectivity(ctx context.Context, url string) error {
	const maxRetries = 3
	const retryInterval = 2 * time.Second

	s.Log.WithField("url", url).Debug("Starting HTTP connectivity validation")

	for attempt := 0; attempt < maxRetries; attempt++ {
		select {
		case <-ctx.Done():
			return fmt.Errorf("context cancelled during HTTP connectivity test")
		default:
		}

		// Create a timeout context for the HTTP request
		reqCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()

		// Create HTTP request
		req, err := http.NewRequestWithContext(reqCtx, "GET", url, nil)
		if err != nil {
			s.Log.WithError(err).Warn("Failed to create HTTP request for connectivity test")
			continue
		}

		// Set appropriate headers
		req.Header.Set("User-Agent", "ESG-NetworkValidator/1.0")
		req.Header.Set("Accept", "application/json")

		// Make HTTP request
		client := &http.Client{
			Timeout: 10 * time.Second,
		}

		resp, err := client.Do(req)
		if err != nil {
			s.Log.WithFields(log.Fields{
				"url":     url,
				"attempt": attempt + 1,
				"error":   err.Error(),
			}).Warn("HTTP connectivity test failed, retrying...")

			// Wait before retrying
			select {
			case <-ctx.Done():
				return fmt.Errorf("context cancelled during HTTP connectivity test")
			case <-time.After(retryInterval):
				// Continue to next attempt
			}
			continue
		}

		resp.Body.Close()

		// Accept any HTTP response (even 404) as it means the service is reachable
		s.Log.WithFields(log.Fields{
			"url":        url,
			"statusCode": resp.StatusCode,
			"attempt":    attempt + 1,
		}).Debug("HTTP connectivity validation successful")
		return nil
	}

	return fmt.Errorf("HTTP connectivity validation failed after %d attempts", maxRetries)
}

// dialWithContext is a helper function for context-aware network dialing
func (s *startBasis) dialWithContext(ctx context.Context, network, address string) (net.Conn, error) {
	var d net.Dialer
	return d.DialContext(ctx, network, address)
}
