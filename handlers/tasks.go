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
	// Note: We don't defer cancel() because generic tasks continue provisioning in background
	// after returning the taskId to the client. The context will timeout naturally.
	startupTime, cancel := context.WithTimeout(context.Background(), config.Conf.ServiceStartupTimeout)
	_ = cancel // Explicitly acknowledge cancel may not be called on all paths (generic tasks need context to continue)

	remote := c.ClientIP()
	l := log.WithField("remote", remote)
	user, password, ok := c.Request.BasicAuth()
	if !ok {
		cancel()
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
		cancel()
		l.WithError(apiErr).WithField("password", password).Warn("Failed to authenticate user on session creation")
		c.Error(utils.AuthErr(fmt.Errorf("invalid username or password"))).SetType(gin.ErrorTypePublic)
		return
	}

	workspace, err := db.GetWorkspace(user)
	if err != nil {
		cancel()
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
		cancel()
		l.WithError(err).Error("Failed to process capabilities")
		c.Error(utils.InvalidArgErr(fmt.Errorf("failed to process capabilities"), err.Error())).SetType(gin.ErrorTypePublic)
		return
	}
	log.Trace("Request capabilitites: ", reqCaps.ToMap())
	log.Trace("Container configuration: ", configurationCaps.ToMap())

	env, routerUUID, err := environment.BuildEnvForTaskDefinitionOverride(workspace, configurationCaps)
	if err != nil {
		cancel()
		log.WithError(err).Error("Failed to build execution environment")
		c.Error(utils.CreationErr(fmt.Errorf("failed to create executor"), err.Error())).SetType(gin.ErrorTypePublic)
		return
	}
	l = l.WithField("family", env.TaskDefinitionFamily).WithField(config.RouterUUID, routerUUID)

	l.Info("new request")

	// Send HTTP headers immediately to prevent client timeout during infrastructure provisioning.
	// This keeps the connection alive by sending periodic whitespace (which JSON parsers ignore)
	// while waiting for the session to be created.
	c.Writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	c.Writer.Header().Set("X-Content-Type-Options", "nosniff")
	// Note: HTTP 200 is sent before session creation completes, so errors are returned in the JSON body (per WebDriver spec).
	c.Writer.WriteHeader(http.StatusOK)
	c.Writer.(http.Flusher).Flush()

	// Channel to receive result from service starter
	type serviceResult struct {
		resp map[string]interface{}
		err  *utils.SeleniumError
	}
	resultCh := make(chan serviceResult, 1)

	// Run service startup in goroutine
	go func() {
		resp, seErr := starter.GetServiceStarter(
			env,
			workspace,
			routerUUID,
			reqCaps,
			c,
			l,
		).StartService(startupTime)
		resultCh <- serviceResult{resp: resp, err: seErr}
	}()

	// Send periodic whitespace to keep connection alive (every 30 seconds)
	// JSON parsers ignore leading whitespace, so this doesn't affect response parsing
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case res := <-resultCh:
			if res.err != nil {
				// WebDriver errors are returned in JSON body, not HTTP status code
				l.WithError(res.err).Error("Service startup failed")
				errorResp := gin.H{
					"value": gin.H{
						"error":   "session not created",
						"message": res.err.Error(),
					},
				}
				json.NewEncoder(c.Writer).Encode(errorResp)
			} else {
				l.WithFields(log.Fields{"resp": res.resp}).Debug("Response")
				json.NewEncoder(c.Writer).Encode(res.resp)
			}
			return
		case <-ticker.C:
			// Send whitespace to keep connection alive - JSON parsers will ignore it
			_, err := c.Writer.Write([]byte(" "))
			if err != nil {
				l.WithError(err).Warn("Failed to send keep-alive, client may have disconnected")
				cancel() // Cancel the startup context to stop provisioning
				return
			}
			c.Writer.(http.Flusher).Flush()
			l.Trace("Sent keep-alive whitespace")
		}
	}
}

func Proxy(c *gin.Context) {
	mapperEntity := c.MustGet(config.RouterUUID).(*mapper.Mapper)
	// c.Request.URL.Path contains router UUID which should be replaced by selenium/selenoid sess.SessionID
	c.Request.URL.Path = rerouteProxy(c.Request.URL.Path, mapperEntity.SessionID)

	url, ok := mapperEntity.Network.GetUrl("driver")
	if !ok {
		log.Error("failed to get `driver` url from session")
		c.Error(utils.UnknownErr(fmt.Errorf("failed to get `driver` url from session"))).SetType(gin.ErrorTypePublic)
		return
	}

	(&httputil.ReverseProxy{
		Director: func(r *http.Request) {
			// fix for file upload using selenium 4
			seUploadPath, uploadPath := "/se/file", "/file"
			if strings.HasSuffix(r.URL.Path, seUploadPath) {
				r.URL.Path = strings.TrimSuffix(r.URL.Path, seUploadPath) + uploadPath
			}

			r.URL.Host, r.URL.Path = url.Host, path.Clean(url.Path+r.URL.Path)
			r.URL.Scheme = "http"
		},
		ErrorHandler: defaultErrorHandler(c),
		ModifyResponse: func(response *http.Response) error {
			contentType := response.Header.Get("Content-Type")
			if contentType != "application/json; charset=utf-8" && contentType != "" {
				response.Header.Set("Content-Type", "application/json; charset=utf-8")
			}
			return nil
		},
	}).ServeHTTP(c.Writer, c.Request)
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

	selenium.CloseSession(mapperEntity)

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
