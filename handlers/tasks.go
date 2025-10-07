package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"path"
	"strings"
	"time"

	"github.com/aerokube/util"
	"github.com/gin-gonic/gin"
	log "github.com/sirupsen/logrus"
	"github.com/zebrunner/esg/cachemaps/mapper"
	"github.com/zebrunner/esg/capabilities"
	"github.com/zebrunner/esg/config"
	"github.com/zebrunner/esg/db"
	"github.com/zebrunner/esg/environment"
	"github.com/zebrunner/esg/selenium"
	"github.com/zebrunner/esg/service"
	"github.com/zebrunner/esg/starter"
	"github.com/zebrunner/esg/utils"
	"github.com/zebrunner/esg/zebrunner"
	"golang.org/x/net/websocket"
)

func Create(c *gin.Context) {
	// start context with deadline before any other action
	startupTime, _ := context.WithTimeout(context.Background(), config.Conf.ServiceStartupTimeout)

	clientIp := c.ClientIP()
	l := log.WithField("clientIp", clientIp)
	user, password, ok := c.Request.BasicAuth()
	if !ok {
		l.Warn("credentials not provided")
		// Hotfix: Selenium java client don't send request with credentials without this sleep.
		// Remove with full migration to Selenium 4.0
		time.Sleep(500 * time.Millisecond)
		c.Error(utils.AuthErr(fmt.Errorf("credentials not provided"))).SetType(gin.ErrorTypePublic)
		return
	}
	l = l.WithField("user", user)

	apiErr := db.CheckAuth(user, password)
	if apiErr != nil {
		l.WithError(apiErr).WithField("password", password).Warn("Failed to authenticate user on session creation")
		c.Error(utils.AuthErr(fmt.Errorf("invalid username or password"))).SetType(gin.ErrorTypePublic)
		return
	}

	workspace, err := db.GetWorkspace(user)
	if err != nil {
		l.Warnf("Workspace for user %s not found", user)
		c.Error(utils.AuthErr(err)).SetType(gin.ErrorTypePublic)
		return
	}

	// not adding workspace to logs because as for now user and workspace have the same value
	// if !config.Conf.SingleTenant {
	// l = l.WithField("workspace", workspace)
	// }

	reqCaps, configurationCaps, err := capabilities.ParseRequestCapabilities(c.Request.Body)
	if err != nil {
		l.WithError(err).Error("Failed to process capabilities")
		c.Error(utils.InvalidArgErr(fmt.Errorf("failed to process capabilities"), err.Error())).SetType(gin.ErrorTypePublic)
		return
	}
	log.Trace("Request capabilitites: ", reqCaps.ToMap())
	log.Trace("Container configuration: ", configurationCaps.ToMap())

	env, routerUUID, err := environment.BuildEnvForTaskDefinitionOverride(workspace, configurationCaps)
	if err != nil {
		log.WithError(err).Error("Failed to build execution environment")
		c.Error(utils.CreationErr(fmt.Errorf("failed to create executor"), err.Error())).SetType(gin.ErrorTypePublic)
		return
	}
	l = l.WithField("family", env.TaskDefinitionFamily).WithField(config.RouterUUID, routerUUID)

	l.Info("new request")

	remoteIp := c.RemoteIP()
	l.Infof("Session create requested clientIp %s, remoteIp %s (user=%s, workspace=%s)", clientIp, remoteIp, user, workspace)

	resp, seErr := starter.GetServiceStarter(
		env,
		workspace,
		routerUUID,
		reqCaps,
		c,
		l,
	).StartService(startupTime)
	if seErr != nil {
		c.Error(seErr).SetType(gin.ErrorTypePublic)
	} else {
		l.WithFields(log.Fields{"resp": resp}).Debug("Response")
		c.JSON(http.StatusOK, resp)
	}
}

func Proxy(c *gin.Context) {
	mapperEntity := c.MustGet(config.RouterUUID).(*mapper.Mapper)
	c.Request.URL.Path = rerouteProxy(c.Request.URL.Path, mapperEntity.SessionID)

	url, ok := mapperEntity.Network.GetUrl("driver")
	if !ok {
		log.Error("failed to get `driver` url from session")
		c.Error(utils.UnknownErr(fmt.Errorf("failed to get `driver` url from session"))).SetType(gin.ErrorTypePublic)
		return
	}

	clientIP := c.ClientIP()
	remoteIP := c.RemoteIP()
	method := c.Request.Method
	l := log.WithFields(log.Fields{
		"routerUUID": mapperEntity.RouterUUID,
		"sessionID":  mapperEntity.SessionID,
		"targetHost": url.Host,
		"clientIP":   clientIP,
		"remoteIP":   remoteIP,
		"method":     method,
		"path":       c.Request.URL.Path,
	})
	l.Debug("Proxy request forwarding")

	// Transport that retries failed TCP or transient requests
	retryTransport := &utils.RetryingTransport{
		Base:    http.DefaultTransport,
		Retries: 2,
		Delay:   500 * time.Millisecond,
	}

	proxy := &httputil.ReverseProxy{
		Director: func(r *http.Request) {
			seUploadPath, uploadPath := "/se/file", "/file"
			if strings.HasSuffix(r.URL.Path, seUploadPath) {
				r.URL.Path = strings.TrimSuffix(r.URL.Path, seUploadPath) + uploadPath
			}

			r.URL.Scheme = "http"
			r.URL.Host = url.Host
			r.URL.Path = path.Clean(url.Path + r.URL.Path)
			r.Host = url.Host
		},
		Transport:    retryTransport,
		ErrorHandler: defaultErrorHandler(c),
		ModifyResponse: func(resp *http.Response) error {
			contentType := resp.Header.Get("Content-Type")
			if contentType != "application/json; charset=utf-8" && contentType != "" {
				resp.Header.Set("Content-Type", "application/json; charset=utf-8")
			}
			l.WithField("status", resp.StatusCode).Trace("Proxy response received")
			return nil
		},
	}

	proxy.ServeHTTP(c.Writer, c.Request)
}

// replace inside c.Request.URL.Path Router UUID by actual selenium/selenoid sessionID for valid routing
func rerouteProxy(path string, sessionId string) string {
	splittedPath := strings.Split(path, "/")
	// path /.../routerUUID/..../...
	if len(splittedPath) < 3 {
		log.Debug("Failed to replace routerUUID with sessionId")
		return path
	}

	splittedPath[2] = sessionId
	return strings.Join(splittedPath, "/")
}

func ProxyMitm(c *gin.Context) {
	mapperEntity := c.MustGet(config.RouterUUID).(*mapper.Mapper)
	url, ok := mapperEntity.Network.GetUrl("proxyHandlerPort")
	if !ok {
		log.Error("failed to get `proxyHandlerPort` url from session")
		c.Error(utils.UnknownErr(fmt.Errorf("failed to get `proxyHandlerPort` url from session"))).SetType(gin.ErrorTypePublic)
		return
	}

	(&httputil.ReverseProxy{
		Director: func(r *http.Request) {
			r.URL.Scheme = "http"
			r.URL.Host = url.Host
			r.Host = url.Host
			r.URL.Path = getRemainingPath(r.URL.Path)
		},
	}).ServeHTTP(c.Writer, c.Request)
}

func CloseSession(c *gin.Context) {
	mapperEntity := c.MustGet(config.RouterUUID).(*mapper.Mapper)

	l := log.WithFields(log.Fields{config.RouterUUID: mapperEntity.RouterUUID, config.TaskIdKey: mapperEntity.TaskId, config.SessionIdKey: mapperEntity.SessionID})

	clientIp := c.ClientIP()
	remoteIp := c.RemoteIP()
	l.Infof("Session DELETE called by clientIp=%s remoteIp=%s routerUUID=%s sessionID=%s", clientIp, remoteIp, mapperEntity.RouterUUID, mapperEntity.SessionID)

	selenium.CloseSession(mapperEntity)

	l.Debugf("Waiting %s for recorder/session to finish...", config.Conf.RecordingShutdownGracePeriod)
	time.Sleep(config.Conf.RecordingShutdownGracePeriod)

	err := service.StopTask(*mapperEntity, mapper.TaskFinished)
	if err != nil {
		l.WithError(err).Warn("Failed to stop task")
	}

	l.Info("task closed")
	c.JSON(http.StatusOK, gin.H{"value": nil})
}

func AbortTask(c *gin.Context) {
	mapperEntity := c.MustGet(config.RouterUUID).(*mapper.Mapper)

	l := log.WithField(config.RouterUUID, mapperEntity.RouterUUID).WithField(config.TaskIdKey, mapperEntity.TaskId)
	if !config.Conf.SingleTenant {
		l = l.WithField("workspace", mapperEntity.Workspace)
	}

	mapperEntity.StopReason = mapper.TaskAborted
	if mapperEntity.TaskId == "" {
		err := mapper.WritedByWorker(mapperEntity, nil, nil, 0)
		if err != nil {
			l.WithError(err).Error("Failed to update task's cache!")
		}
	} else {
		err := service.StopTask(*mapperEntity, mapperEntity.StopReason)
		if err != nil {
			l.WithError(err).Warn("Failed to stop task")
		}
	}

	l.Info("task aborted")
	c.JSON(http.StatusNoContent, gin.H{})
}

func MarkAsFinished(c *gin.Context) {
	mapperEntity := c.MustGet(config.RouterUUID).(*mapper.Mapper)

	l := log.WithField(config.RouterUUID, mapperEntity.RouterUUID).WithField(config.TaskIdKey, mapperEntity.TaskId)
	if !config.Conf.SingleTenant {
		l = l.WithField("workspace", mapperEntity.Workspace)
	}

	m, err := mapper.Find(mapperEntity.RouterUUID)
	if err == nil && m.Status != mapper.Stopped {
		mapperEntity.Status = mapper.Stopped
		mapperEntity.StopReason = mapper.TaskFinished
		err = mapper.Write(mapperEntity, -1)
		if err != nil {
			log.WithError(err).Error("Failed to mark generec task as stopped in cache")
		}
	}

	go func() {
		zebrunner.AbortLaunch(mapperEntity.RouterUUID, mapperEntity.Workspace, mapperEntity.Capabilities.LaunchUUID.ToPrimitive(), "Executor finished")
		l.Info("Generic task finished")
	}()

	c.JSON(http.StatusNoContent, gin.H{})
}

func Vnc(c *gin.Context) {
	mapperEntity := c.MustGet(config.RouterUUID).(*mapper.Mapper)
	l := log.WithField(config.RouterUUID, mapperEntity.RouterUUID)
	l.Debug("Vnc request")

	vncUrl, ok := mapperEntity.Network.GetUrl("vnc")
	if !ok {
		err := fmt.Errorf("vnc url is not available")
		l.WithError(err).WithField("url", vncUrl).Warn("vnc error")

		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.Request.Header.Add("Access-Control-Allow-Origin", "*")
	c.Request.Header.Add("X-Real-IP", c.Request.RemoteAddr)

	websocket.Handler(
		func(wsconn *websocket.Conn) {
			defer wsconn.Close()

			l.Debug("vnc enabled")
			var d net.Dialer
			conn, err := d.DialContext(wsconn.Request().Context(), "tcp", vncUrl.Host)
			if err != nil {
				l.WithError(err).Error("vnc error")
				return
			}
			defer conn.Close()
			wsconn.PayloadType = websocket.BinaryFrame
			go func() {
				defer wsconn.Close()
				_, e := io.Copy(wsconn, conn)
				if e != nil {
					log.WithError(e).Debug("VNC WS Copy error")
				}
				l.Debug("vnc session closed")
			}()
			_, err = io.Copy(conn, wsconn)
			if err != nil {
				log.WithError(err).Debug("VNC WS Copy error")
			}
			l.Debug("vnc client disconected")
		},
	).ServeHTTP(c.Writer, c.Request)
}

func Logs(c *gin.Context) {
	user, _, ok := c.Request.BasicAuth()
	if !ok {
		c.Error(utils.AuthApiErr("auth data not provided")).SetType(gin.ErrorTypePublic)
		return
	}

	routerUUID := c.Param("session")
	logFile := strings.Join([]string{user, "artifacts", "test-sessions", routerUUID, "session.log"}, "/")
	presignedUrl, err := service.GeneratePreSignedURL(logFile)
	if err != nil {
		log.Printf("[URL GENERATION FAILED] %v", err)
		c.Error(utils.NotFoundApiErr("resource not found")).SetType(gin.ErrorTypePublic)
		return
	}

	c.Redirect(http.StatusFound, presignedUrl)
}

func Video(c *gin.Context) {
	user, _, ok := c.Request.BasicAuth()
	if !ok {
		c.Error(utils.AuthApiErr("auth data not provided")).SetType(gin.ErrorTypePublic)
		return
	}

	routerUUID := c.Param("session")
	videoFile := strings.Join([]string{user, "artifacts", "test-sessions", routerUUID, "video.mp4"}, "/")
	presignedUrl, err := service.GeneratePreSignedURL(videoFile)
	if err != nil {
		log.WithError(err).WithFields(log.Fields{
			"user":              user,
			"remote":            c.ClientIP(),
			config.SessionIdKey: routerUUID,
		}).Error("Failed to create pre signed url to session video")

		c.Error(utils.NotFoundApiErr("resource not found")).SetType(gin.ErrorTypePublic)
		return
	}

	c.Redirect(http.StatusFound, presignedUrl)
}

func TaskLog(c *gin.Context) {
	user, _, ok := c.Request.BasicAuth()
	if !ok {
		c.Error(utils.AuthApiErr("auth data not provided")).SetType(gin.ErrorTypePublic)
		return
	}

	routerUUID := c.Param("task")
	logFile := strings.Join([]string{user, "artifacts", "launches", routerUUID, "console.log"}, "/")
	presignedUrl, err := service.GeneratePreSignedURL(logFile)
	if err != nil {
		log.Printf("[URL GENERATION FAILED] %v", err)
		c.Error(utils.NotFoundApiErr("resource not found")).SetType(gin.ErrorTypePublic)
		return
	}

	c.Redirect(http.StatusFound, presignedUrl)
}

func TaskDescribe(c *gin.Context) {
	user, _, ok := c.Request.BasicAuth()
	if !ok {
		c.Error(utils.AuthApiErr("auth data not provided")).SetType(gin.ErrorTypePublic)
		return
	}

	routerUUID := c.Param("task")
	l := log.WithField(config.RouterUUID, routerUUID).WithField("user", user)

	// Think about better impl
	var apiErr *utils.APIError
	mapperEntity, err := mapper.Find(routerUUID)
	if err != nil || mapperEntity == nil {
		apiErr = utils.NotFoundApiErr("session timed out or not found")
	} else if mapperEntity.Status == mapper.Queued {
		apiErr = utils.NotFoundApiErr("session creation is in queue")
	} else if mapperEntity.Status == mapper.Stopped {
		apiErr = utils.NotFoundApiErr(string(mapperEntity.StopReason))
	}

	if apiErr != nil {
		l.Error("Failed to get task status")
		c.Error(utils.NotFoundApiErr(apiErr.Error())).SetType(gin.ErrorTypePublic)
		return
	}

	result, err := service.DescribeTask(mapperEntity.TaskId)

	if err != nil {
		l.Error("Failed to get task status")
		c.Error(utils.UnknownApiErr(fmt.Sprintf("failed to get task status: %v", err.Error()))).
			SetType(gin.ErrorTypePublic)
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": result.Tasks[0].LastStatus})
}

func Downloads(c *gin.Context) {
	mapperEntity := c.MustGet(config.RouterUUID).(*mapper.Mapper)
	// Because of the awsvpc network mode, we may need additional implementation for the MITM file server connection
	url, ok := mapperEntity.Network.GetUrl("fileserver")
	if !ok {
		log.Error("failed to get `fileserver` url from session")
		c.Error(utils.UnknownErr(fmt.Errorf("failed to get `fileserver` url from session"))).SetType(gin.ErrorTypePublic)
		return
	}

	director := func(req *http.Request) {
		req.URL.Scheme = "http"
		req.URL.Host = url.Host
		req.Host = url.Host
		req.URL.Path = getRemainingPath(req.URL.Path)
	}
	proxy := &httputil.ReverseProxy{Director: director}

	proxy.ServeHTTP(c.Writer, c.Request)
}

func Clipboard(c *gin.Context) {
	mapperEntity := c.MustGet(config.RouterUUID).(*mapper.Mapper)
	url, ok := mapperEntity.Network.GetUrl("clipboard")
	if !ok {
		log.Error("failed to get `clipboard` url from session")
		c.Error(utils.UnknownErr(fmt.Errorf("failed to get `clipboard` url from session"))).SetType(gin.ErrorTypePublic)
		return
	}

	proxy := &httputil.ReverseProxy{
		Director: func(req *http.Request) {
			req.URL.Scheme = "http"
			req.URL.Host = url.Host
			req.Host = url.Host
		},
	}
	proxy.ServeHTTP(c.Writer, c.Request)
}

func Devtools(c *gin.Context) {
	mapperEntity := c.MustGet(config.RouterUUID).(*mapper.Mapper)
	url, ok := mapperEntity.Network.GetUrl("devtools")
	if !ok {
		log.Error("failed to get `devtools` url from session")
		c.Error(utils.UnknownErr(fmt.Errorf("failed to get `devtools` url from session"))).SetType(gin.ErrorTypePublic)
		return
	}

	director := func(req *http.Request) {
		req.URL.Scheme = "http"
		req.URL.Host = url.Host
		req.Host = url.Host
		req.URL.Path = getRemainingPath(req.URL.Path)
	}
	proxy := &httputil.ReverseProxy{Director: director}
	proxy.ServeHTTP(c.Writer, c.Request)
}

func getRemainingPath(path string) string {
	pathFragments := strings.Split(path, "/")
	//Path= /devtools/:session/...
	if len(pathFragments) < 4 {
		return "/"
	}

	return "/" + strings.Join(pathFragments[3:], "/")
}

func defaultErrorHandler(c *gin.Context) func(http.ResponseWriter, *http.Request, error) {
	return func(w http.ResponseWriter, r *http.Request, err error) {
		user, remote := util.RequestInfo(r)
		log.WithError(err).WithFields(log.Fields{
			"user":   user,
			"remote": remote,
		}).Error("Client disconnected")
		w.WriteHeader(http.StatusInternalServerError)
		driverError := gin.H{
			"value": gin.H{
				"error":   "unknown error",
				"message": "Driver connection refused",
			},
		}
		_ = json.NewEncoder(w).Encode(driverError)
	}
}
