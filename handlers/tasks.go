package handlers

import (
	"context"
	"encoding/json"
	"errors"
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
	"github.com/zebrunner/esg/cachemaps/sessionmap"
	"github.com/zebrunner/esg/cachemaps/taskmap"
	"github.com/zebrunner/esg/capabilities"
	"github.com/zebrunner/esg/config"
	"github.com/zebrunner/esg/environment"
	"github.com/zebrunner/esg/selenium"
	"github.com/zebrunner/esg/service"
	"github.com/zebrunner/esg/utils"
	"golang.org/x/net/websocket"
)

func getSession(id string) (*sessionmap.Session, *utils.SeleniumError) {
	session, _ := sessionmap.Find(id, true)
	if session == nil {
		return nil, utils.NoSuchSessionErr(errors.New("session timed out or not found"))
	}

	if session.Status == sessionmap.SessionStopped {
		return nil, utils.SessionStoppedErr(errors.New(string(session.StopReason)))
	}

	return session, nil
}

func getTask(id string) (*taskmap.Task, *utils.SeleniumError) {
	task, _ := taskmap.Find(id)
	if task == nil {
		return nil, utils.NoSuchTaskErr(errors.New("task timed out or not found"))
	}

	if task.Status == taskmap.TaskStopped {
		return nil, utils.TaskStoppedErr(errors.New(string(task.StopReason)))
	}

	return task, nil
}

func Create(c *gin.Context) {
	remote := c.ClientIP()
	user, password, _ := c.Request.BasicAuth()
	workspace, err := service.GetWorkspace(user)
	if err != nil {
		// Hotfix: Selenium java client don't send request with credentials without this sleep.
		// Remove with full migration to Selenium 4.0
		time.Sleep(500 * time.Millisecond)

		c.Error(utils.AuthErr(err)).SetType(gin.ErrorTypePublic)
		return
	}

	apiErr := service.CheckAuth(user, password)
	if apiErr != nil {
		log.WithError(apiErr).WithFields(log.Fields{
			"client":   c.ClientIP(),
			"user":     user,
			"password": password,
		}).Warn("Failed to authenticate user on session creation")

		c.Error(utils.AuthErr(errors.New("invalid username or password"))).SetType(gin.ErrorTypePublic)
		return
	}

	l := log.WithFields(log.Fields{"user": user, "remote": remote})

	var taskCaps capabilities.RequestCaps
	err = c.BindJSON(&taskCaps)
	if err != nil {
		l.WithError(err).Error("Failed to bind json to browser struct")

		c.Error(utils.InvalidArgErr(fmt.Errorf("bad JSON format: %v", err))).SetType(gin.ErrorTypePublic)
		return
	}

	if len(taskCaps.DesiredCapabilities) != 0 {
		err = taskCaps.ProcessLegacy()
	} else {
		err = taskCaps.Process()
	}
	if err != nil {
		l.WithError(err).Error("Failed to process capabilities")

		c.Error(utils.InvalidArgErr(fmt.Errorf("failed to process capabilities: %v", err))).SetType(gin.ErrorTypePublic)
		return
	}
	log.Trace("Driver capabilitites: ", taskCaps.ToMap())

	caps, err := taskCaps.GetContainerConfiguration()
	if err != nil {
		l.WithError(err).Error("Failed to get container config.Configuration")

		c.Error(utils.InvalidArgErr(fmt.Errorf("failed to process capabilities: %v", err))).SetType(gin.ErrorTypePublic)
		return
	}
	log.Trace("caps: ", caps)

	sessionStartTime := time.Now()
	ctx, ctxCancel := context.WithTimeout(context.Background(), config.Conf.ServiceStartupTimeout)
	defer ctxCancel()

	env, err := environment.Build(user, caps)
	if err != nil {
		log.WithError(err).Error("Failed to build execution environment")

		c.Error(utils.CreationErr(fmt.Errorf("failed to start executor: %v", err))).SetType(gin.ErrorTypePublic)
		return
	}
	env.RawCapabilities = &taskCaps
	env.Workspace = workspace

	l = log.WithFields(log.Fields{"remote": remote, "family": env.TaskDefinitionFamily})
	if !config.Conf.SingleTenant {
		l = l.WithField("workspace", workspace)
	}

	l.Info("new request")
	l.WithField("env", env).Debug("Env details")

	if env.TaskDefinitionFamily == "generic" {
		_, err = service.CreateTaskDefinition(env)
		if err != nil {
			log.WithError(err).Error("Failed to create task definition")
			return
		}
	}

	taskCache, err := service.StartTask(ctx, env)
	if err != nil {
		err = fmt.Errorf("service startup failed: %v", err)
		l.Error(err)

		c.Error(utils.CreationErr(err)).SetType(gin.ErrorTypePublic)
		return
	}

	l = l.WithField("_taskId", taskCache.ID)
	l.Info("task started")
	var resp map[string]interface{}
	if env.TaskDefinitionFamily == "generic" || strings.HasPrefix(env.TaskDefinitionFamily, "cypress") {
		// TODO: delete status update when CloseSession() for generic tasks will be called
		taskCache.Status = taskmap.TaskGeneric
		err := taskmap.Write(taskCache.ID, taskCache, 0)
		if err != nil {
			l.WithError(err).Error("Failed to update generic's status")
		}
		data := "{\"taskId\": \"" + taskCache.ID + "\"}"
		json.Unmarshal([]byte(data), &resp)
		l.WithFields(log.Fields{"resp": resp}).Debug("Response")
	} else {
		driverCtx, driverCtxCancel := context.WithTimeout(context.Background(), config.Conf.DriverStartupTimeout)
		defer driverCtxCancel()
		u, ok := env.Network.GetUrl("driver")
		if !ok {
			l.Error("failed to get url for `driver` service")

			c.Error(utils.CreationErr(fmt.Errorf("failed to start driver: %v", err))).SetType(gin.ErrorTypePublic)
			return
		}

		requestBody, err := json.Marshal(env.RawCapabilities)
		if err != nil {
			l.WithError(err).Error("Failed to marshal request")

			c.Error(utils.UnknownErr(fmt.Errorf("failed to marshal capabilities: %v", err))).SetType(gin.ErrorTypePublic)
			return
		}

		c.Request.URL.Host, c.Request.URL.Path = u.Host, path.Join(u.Path, c.Request.URL.Path)
		c.Request.URL.Scheme = "http"
		l.WithField("serviceUrl", u).Debug("driver starting")

		resp, err = selenium.StartSession(driverCtx, c.Request.URL, c.Request.Header, requestBody)
		if err != nil {
			if strings.Contains(err.Error(), context.DeadlineExceeded.Error()) {
				err = errors.New("driver startup timed out")
			}
			l.WithError(err).WithField("response", resp).Error("driver startup failed")

			c.Error(utils.CreationErr(fmt.Errorf("failed to start driver: %v", err))).SetType(gin.ErrorTypePublic)

			err = service.StopTask(taskCache.ID, taskmap.SessiongStartupFailure)
			if err != nil {
				l.WithError(err).Warn("Failed to stop task")
			}
			return
		}

		sessionId, err := getSessionId(resp)
		if sessionId == "" {
			if err == nil {
				err = errors.New("session id in driver response is empty")
			}
			l.WithError(err).Error("Failed to get sessionId")

			c.Error(utils.CreationErr(fmt.Errorf("failed to create driver: %v", err))).SetType(gin.ErrorTypePublic)

			err = service.StopTask(taskCache.ID, taskmap.SessiongStartupFailure)
			if err != nil {
				l.WithError(err).Warn("Failed to stop task")
			}
			return
		}

		sessionCache, err := sessionmap.CreateEntity(sessionId, env, taskCache)
		if err != nil {
			l.WithError(err).Error("Failed to cache driver session")

			c.Error(utils.UnknownErr(fmt.Errorf("failed to cache driver session: %v", err))).SetType(gin.ErrorTypePublic)

			err = service.StopTask(taskCache.ID, taskmap.SessiongStartupFailure)
			if err != nil {
				l.WithError(err).Warn("Failed to stop task")
			}
			return
		}
		l.WithField("sessionId", sessionCache.ID).WithField("latency", util.SecondsSince(sessionStartTime)).Info("driver started")
	}

	c.JSON(http.StatusOK, resp)
}

func Proxy(c *gin.Context) {
	sessionID := c.Param("session")
	sess, seErr := getSession(sessionID)
	if seErr != nil {
		log.WithError(seErr).WithField("sessionId", sessionID).Error("Proxy(): can't access session")
		c.Error(seErr).SetType(gin.ErrorTypePublic)
		return
	}

	(&httputil.ReverseProxy{
		Director: func(r *http.Request) {
			url, ok := sess.Network.GetUrl("driver")
			if !ok {
				log.Error("failed to get `driver` url from session")
				c.Error(utils.UnknownErr(fmt.Errorf("failed to get `driver` url from session"))).SetType(gin.ErrorTypePublic)
			}

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
func CloseSession(c *gin.Context) {
	sessionId := c.Param("session")
	sess, seErr := getSession(sessionId)
	if seErr != nil {
		log.WithError(seErr).WithField("sessionId", sessionId).Error("CloseSession(): can't access session")
		c.Error(seErr).SetType(gin.ErrorTypePublic)
		return
	}
	l := log.WithField("_taskId", sess.TaskID)

	selenium.CloseSession(sess, sessionmap.SessionFinished)
	l = l.WithField("sessionId", sess.ID)

	err := service.StopTask(sess.TaskID, taskmap.TaskFinished)
	if err != nil {
		l.WithError(err).Warn("Failed to stop task")
	}

	l.Info("task closed")
	c.JSON(http.StatusOK, gin.H{"value": nil})
}

func AbortTask(c *gin.Context) {
	taskId := c.Param("task")
	l := log.WithField("_taskId", taskId)

	task, seErr := getTask(taskId)
	if seErr != nil {
		l.WithError(seErr).Error("AbortTask(): can't access task")
		c.Error(seErr).SetType(gin.ErrorTypePublic)
		return
	}

	if !config.Conf.SingleTenant {
		l = l.WithField("workspace", task.Workspace)
	}

	err := service.StopTask(task.ID, taskmap.TaskAborted)
	if err != nil {
		l.WithError(err).Warn("Failed to stop task")
	}

	l.Info("task aborted")
	c.JSON(http.StatusNoContent, gin.H{})
}

func Vnc(wsconn *websocket.Conn) {
	defer wsconn.Close()
	fragments := strings.Split(wsconn.Request().URL.Path, "/")
	sid := fragments[len(fragments)-1]
	l := log.WithField("sessionId", sid)

	sess, seErr := getSession(sid)
	if seErr != nil {
		l.WithError(seErr).Error("Vnc(): can't access session")
		return
	}
	log.Debug("sess.Network: ", sess.Network)

	vncUrl, ok := sess.Network.GetUrl("vnc")
	if !ok {
		l.Warn("Vnc url is not available: ", vncUrl)
		return
	}

	l.Debug("Vnc enabled")
	var d net.Dialer
	conn, err := d.DialContext(wsconn.Request().Context(), "tcp", vncUrl.Host)
	if err != nil {
		l.WithError(err).Error("Vnc error")
		return
	}
	defer conn.Close()
	wsconn.PayloadType = websocket.BinaryFrame
	go func() {
		_, e := io.Copy(wsconn, conn)
		if e != nil {
			log.WithError(e).Debug("VNC WS Copy error")
		}
		wsconn.Close()
		l.Debug("Vnc session closed")
	}()
	_, err = io.Copy(conn, wsconn)
	if err != nil {
		log.WithError(err).Debug("VNC WS Copy error")
	}
	l.Debug("Vnc client disconected")
}

func Logs(c *gin.Context) {
	user, _, ok := c.Request.BasicAuth()
	if !ok {
		c.Error(utils.AuthApiErr("auth data not provided")).SetType(gin.ErrorTypePublic)
		return
	}

	sessionID := c.Param("session")
	logFile := strings.Join([]string{user, "artifacts", "test-sessions", sessionID, "session.log"}, "/")
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

	sessionID := c.Param("session")
	videoFile := strings.Join([]string{user, "artifacts", "test-sessions", sessionID, "video.mp4"}, "/")
	presignedUrl, err := service.GeneratePreSignedURL(videoFile)
	if err != nil {
		log.WithError(err).WithFields(log.Fields{
			"user":      user,
			"remote":    c.ClientIP(),
			"sessionId": sessionID,
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

	taskID := c.Param("task")
	logFile := strings.Join([]string{user, "artifacts", "launches", taskID, "console.log"}, "/")
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

	taskId := c.Param("task")
	l := log.WithField("user", user).WithField("_taskId", taskId)
	l.Debug("Get task status")
	result, err := service.DescribeTask(taskId)
	if err != nil {
		l.Error("Failed to get task status")
		c.Error(utils.UnknownApiErr(fmt.Sprintf("failed to get task status: %v", err.Error()))).
			SetType(gin.ErrorTypePublic)
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": result.Tasks[0].LastStatus})
}

func Downloads(c *gin.Context) {
	sessionID := c.Param("session")
	filename := c.Param("file")
	sess, seErr := getSession(sessionID)
	if seErr != nil {
		log.WithError(seErr).WithField("sessionId", sessionID).Error("Downloads(): can't access session")
		c.Error(seErr).SetType(gin.ErrorTypePublic)
		return
	}

	director := func(req *http.Request) {
		req.URL.Scheme = "http"
		if sess != nil {
			url, _ := sess.Network.GetUrl("fileserver")
			req.URL.Host = url.Host
			req.Host = url.Host
			req.URL.Path = "/" + filename
		}
	}
	proxy := &httputil.ReverseProxy{Director: director}
	fmt.Println(c.Request)
	proxy.ServeHTTP(c.Writer, c.Request)
}

func Clipboard(c *gin.Context) {
	sessionID := c.Param("session")
	sess, seErr := getSession(sessionID)
	if seErr != nil {
		log.WithError(seErr).WithField("sessionId", sessionID).Error("Clipboard(): can't access session")
		_ = c.Error(seErr).SetType(gin.ErrorTypePublic)
		return
	}
	director := func(req *http.Request) {
		req.URL.Scheme = "http"
		url, _ := sess.Network.GetUrl("clipboard")
		req.URL.Host = url.Host
		req.Host = url.Host
	}
	proxy := &httputil.ReverseProxy{Director: director}
	proxy.ServeHTTP(c.Writer, c.Request)
}

func Devtools(c *gin.Context) {
	sessionID := c.Param("session")
	sess, seErr := getSession(sessionID)
	if seErr != nil {
		log.WithError(seErr).WithField("sessionId", sessionID).Error("Devtools(): can't access session")
		_ = c.Error(seErr).SetType(gin.ErrorTypePublic)
		return
	}

	director := func(req *http.Request) {
		req.URL.Scheme = "http"
		url, _ := sess.Network.GetUrl("devtools")
		req.URL.Host = url.Host
		req.Host = url.Host
		req.Header.Set("Access-Control-Allow-Origin", "*")
	}
	proxy := &httputil.ReverseProxy{Director: director}
	proxy.ServeHTTP(c.Writer, c.Request)
}

func defaultErrorHandler(с *gin.Context) func(http.ResponseWriter, *http.Request, error) {
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
