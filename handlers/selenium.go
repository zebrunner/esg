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
	"net/url"
	"path"
	"strings"
	"time"

	"github.com/aerokube/util"
	"github.com/gin-gonic/gin"
	log "github.com/sirupsen/logrus"
	"github.com/zebrunner/esg/capabilities"
	"github.com/zebrunner/esg/config"
	"github.com/zebrunner/esg/environment"
	"github.com/zebrunner/esg/selenium"
	"github.com/zebrunner/esg/service"
	sessionmap "github.com/zebrunner/esg/sessinonmap"
	"github.com/zebrunner/esg/utils"
	"golang.org/x/net/websocket"
)

func getSession(id string) (*sessionmap.Session, error) {
	session, err := sessionmap.Find(id, true)
	if err != nil {
		return nil, err
	}

	if session.Status == sessionmap.SessionStoppedIdle {
		return nil, &utils.SeleniumError{
			ResponseStatus: http.StatusNotFound,
			SeleniumCode:   "invalid session id",
			Message:        fmt.Sprintf("Session stopped due IDLE timeout"),
			Err:            err,
		}
	}

	return session, nil
}

func Create(c *gin.Context) {
	remote := c.ClientIP()
	user, password, _ := c.Request.BasicAuth()
	workspace, err := service.GetWorkspace(user)
	if err != nil {
		// Hotfix: Selenium java client don't send request with credentials without this sleep.
		// Remove with full migration to Selenium 4.0
		time.Sleep(500 * time.Millisecond)
		_ = c.Error(&utils.SeleniumError{
			SeleniumCode:   "session not created",
			ResponseStatus: http.StatusUnauthorized,
			Message:        "Session not created; Reason: Failed to get auth credentials.",
		}).SetType(gin.ErrorTypePublic)
		return
	}

	err = service.CheckAuth(user, password)
	if err != nil {
		log.WithError(err).WithFields(log.Fields{
			"client":   c.ClientIP(),
			"user":     user,
			"password": password,
		}).Warn("Failed to authenticate user on session creation")
		_ = c.Error(&utils.SeleniumError{
			SeleniumCode:   "session not created",
			ResponseStatus: http.StatusUnauthorized,
			Message:        "Session not created; Reason: Invalid username or password",
		}).SetType(gin.ErrorTypePublic)
		return
	}

	l := log.WithFields(log.Fields{"user": user, "remote": remote})

	var body capabilities.RequestCaps
	err = c.BindJSON(&body)
	if err != nil {
		l.WithError(err).Error("Failed to bind json to browser struct")
		_ = c.Error(creationError("Bad JSON format", err)).SetType(gin.ErrorTypePublic)
		return
	}

	processingError := utils.SeleniumError{
		SeleniumCode:   "invalid argument",
		ResponseStatus: http.StatusBadRequest,
		Message:        "Failed to process capabilities. ",
	}
	err = body.ProcessLegacy()
	if err != nil {
		l.WithError(err).Error("Failed to process capabilities")
		_ = c.Error(&processingError).SetType(gin.ErrorTypePublic)
		return
	}

	caps, err := body.GetContainerConfiguration()
	if err != nil {
		l.WithError(err).Error("Failed to get container config.Configuration")
		_ = c.Error(&processingError).SetType(gin.ErrorTypePublic)
		return
	}

	sessionStartTime := time.Now()
	ctx, ctxCancel := context.WithTimeout(context.Background(), config.Conf.ServiceStartupTimeout)
	defer ctxCancel()

	env, err := environment.Build(user, caps, &config.Conf)
	if err != nil {
		log.WithError(err).Error("Failed to build execution environment")
		_ = c.Error(creationError("failed to start executor", err)).SetType(gin.ErrorTypePublic)
		return
	}

	//l.WithField("env", env).Info("Execution env")

        if env.TaskDefinitionFamily == "generic" {
	        _, err = service.CreateGenericTaskDefinition(env)
        	if err != nil {
                	log.WithError(err).Error("Failed to create task definition")
	                return
        	}
        }

	err = service.StartDriver(ctx, env)
	if err == context.DeadlineExceeded {
		err = errors.New("Driver startup timed out")
	}

	if err != nil {
		l.WithError(err).Error("Service startup failed")
		_ = c.Error(creationError("Failed to start driver", err)).SetType(gin.ErrorTypePublic)
		return
	}
	// l.WithField("taskID", driver.TaskID).Info("Service started successfully")
	u, ok := env.Network.GetUrl("driver")
	if !ok {
		l.Error("failed to get url for `driver` service")
		_ = c.Error(creationError("Failed to start driver", err)).SetType(gin.ErrorTypePublic)
		return
	}

	requestBody, err := json.Marshal(body)
	if err != nil {
		l.WithError(err).Error("Failed to marshal request")
		_ = c.Error(creationError("Failed to start driver", err)).SetType(gin.ErrorTypePublic)
		return
	}

	sessionId := ""
	//TODO: implement reponse for generic task
	var resp map[string]interface{}
        if env.TaskDefinitionFamily == "generic" {
		sessionId = env.TaskId
		data := "{\"taskId\": \"" + env.TaskId + "\", \"log\": \"qwe\"}"
		json.Unmarshal([]byte(data), &resp)
		l.WithFields(log.Fields{"resp": resp,}).Info("Response")
	} else {
		c.Request.URL.Host, c.Request.URL.Path = u.Host, path.Join(u.Path, c.Request.URL.Path)
		c.Request.URL.Scheme = "http"
		l.WithFields(log.Fields{"serviceUrl": u,}).Info("Starting session")
		resp, err = selenium.StartSession(c.Request.Context(), c.Request.URL, c.Request.Header, requestBody)
		if err != nil {
			l.WithError(err).WithField("response", resp).Error("Session startup failed")
			c.JSON(http.StatusInternalServerError, resp)
			service.RemoveTask(env.TaskId)
			return
		}

		sessionId, err = getSessionId(resp)
		if err != nil {
			l.WithError(err).Error("Failed to get sessionId from driver response")
			_ = c.Error(creationError("failed to create session", err)).SetType(gin.ErrorTypePublic)
			return
		}

		if sessionId == "" {
			l.WithError(err).Error("Failed to get sessionId from driver response. sessionId is empty")
			_ = c.Error(creationError("failed to create session", err)).SetType(gin.ErrorTypePublic)
			return
		}
	}

	sess := sessionmap.Session{
		ID:              sessionId,
		RawCapabilities: body.ToMap(),
		Capabilities:    *caps,
		Network:         *env.Network,
		StartedAt:       time.Now(),
		AccessedAt:      time.Now(),
		Status:          sessionmap.SessionActive,
		TaskID:          env.TaskId,
		Workspace:       workspace,
	}
	if env.TaskDefinitionFamily == "generic" {
		// extra state for generic job to disable idleTimeout verification at all
		sess.Status = sessionmap.SessionQueued
	}

	err = sessionmap.Write(sess.ID, &sess, 0)
	if err != nil {
		l.WithError(err).Error("Session not cached")
	}
	l.WithFields(log.Fields{
		"sessionId": sess.ID,
		"taskId": sess.TaskID,
		"latency":   util.SecondsSince(sessionStartTime),
	}).Info("Session created")

	c.JSON(http.StatusOK, resp)
}

func Proxy(c *gin.Context) {
	sessionID := c.Param("session")
	sess, err := getSession(sessionID)
	if err != nil {
		log.WithError(err).WithField("sessionID", sessionID).Error("Cant find session")
		_ = c.Error(err).SetType(gin.ErrorTypePublic)
		return
	}

	(&httputil.ReverseProxy{
		Director: func(r *http.Request) {
			url, ok := sess.Network.GetUrl("driver")
			if !ok {
				log.Error("failed to get `driver` url from session")
				_ = c.Error(fmt.Errorf("internal error")).SetType(gin.ErrorTypePublic)
			}
			r.URL.Host, r.URL.Path = url.Host, path.Clean(url.Path+r.URL.Path)
			r.URL.Scheme = "http"
		},
		ErrorHandler: defaultErrorHandler(c),
	}).ServeHTTP(c.Writer, c.Request)
}

func CloseSession(c *gin.Context) {
	sessionId := c.Param("session")
	sess, err := getSession(sessionId)
	if err != nil {
		log.WithError(err).WithField("sessionID", sessionId).Error("Cant find session")
		_ = c.Error(err).SetType(gin.ErrorTypePublic)
		return
	}
	selenium.CloseSession(sess.Workspace, sess.ID, &config.Conf)
	service.RemoveTask(sess.TaskID)

	err = sessionmap.Remove(sessionId)
	if err != nil {
		log.WithError(err).WithField("sessionID", sessionId).Error("Failed to remove session from session map")
		_ = c.Error(err).SetType(gin.ErrorTypePublic)
		return
	}
	log.WithField("sessionID", sessionId).Info("Session closed")
	c.JSON(http.StatusOK, gin.H{"value": nil})
}

func Vnc(wsconn *websocket.Conn) {
	defer wsconn.Close()
	fragments := strings.Split(wsconn.Request().URL.Path, "/")
	sid := fragments[len(fragments)-1]
	l := log.WithField("sessionID", sid)
	sess, err := getSession(sid)

	if err != nil {
		l.WithError(err).Error("Session not found")
		return
	}

	vncUrl, ok := sess.Network.GetUrl("vnc")
	if !ok {
		l.Debug("Vnc not enabled")
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
			log.WithError(e).Error("VNC WS Copy error")
		}
		wsconn.Close()
		l.Debug("Vnc session closed")
	}()
	_, err = io.Copy(conn, wsconn)
	if err != nil {
		log.WithError(err).Error("VNC WS Copy error")
	}
	l.Debug("Vnc client disconected")
}

func Logs(c *gin.Context) {
	user, _, ok := c.Request.BasicAuth()
	if config.Conf.TrustedMode {
		user = "zebrunner"
		ok = true
	}

	if !ok {
		_ = c.Error(&utils.HTTPError{
			Status:  http.StatusBadRequest,
			Message: "Auth data not provided"},
		).SetType(gin.ErrorTypePublic)
		return
	}
	sessionID := c.Param("session")
	logFile := strings.Join([]string{user, "artifacts", "test-sessions", sessionID, "session.log"}, "/")
	presignedUrl, err := service.GeneratePreSignedURL(logFile)
	if err != nil {
		log.Printf("[URL GENERATION FAILED] %v", err)
		return
	}
	c.Redirect(http.StatusFound, presignedUrl)
}

func Video(c *gin.Context) {
	user, _, ok := c.Request.BasicAuth()
	if config.Conf.TrustedMode {
		user = "zebrunner"
		ok = true
	}
	if !ok {
		_ = c.Error(&utils.HTTPError{
			Status:  http.StatusBadRequest,
			Message: "Auth data not provided"},
		).SetType(gin.ErrorTypePublic)
		return
	}
	sessionID := c.Param("session")
	videoFile := strings.Join([]string{user, "artifacts", "test-sessions", sessionID, "video.mp4"}, "/")
	presignedUrl, err := service.GeneratePreSignedURL(videoFile)
	if err != nil {
		log.WithError(err).WithFields(log.Fields{
			"user":      user,
			"remote":    c.ClientIP(),
			"sessionID": sessionID,
		}).Error("Failed to create pre signed url to session video")
		_ = c.Error(err)
		return
	}
	c.Redirect(http.StatusFound, presignedUrl)
}

func Downloads(c *gin.Context) {
	sessionID := c.Param("session")
	filename := c.Param("file")
	sess, err := getSession(sessionID)
	if err != nil {
		_ = c.Error(err)
		return
	}

	director := func(req *http.Request) {
		req.URL.Scheme = "http"
		url, _ := sess.Network.GetUrl("fileserver")
		req.URL.Host = url.Host
		req.Host = url.Host
		req.URL.Path = "/" + filename
	}
	proxy := &httputil.ReverseProxy{Director: director}
	fmt.Println(c.Request)
	proxy.ServeHTTP(c.Writer, c.Request)
}

func Clipboard(c *gin.Context) {
	sessionID := c.Param("session")
	sess, err := getSession(sessionID)
	if err != nil {
		_ = c.Error(err)
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
	sess, err := getSession(sessionID)
	if err != nil {
		_ = c.Error(err)
		return
	}

	u, _ := sess.Network.GetUrl("devtools")
	fileUrl := url.URL{
		Host: u.Host,
	}
	c.Redirect(http.StatusFound, fileUrl.String())
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

func creationError(msg string, err error) *utils.SeleniumError {
	return &utils.SeleniumError{
		SeleniumCode:   "session not created",
		ResponseStatus: http.StatusInternalServerError,
		Message:        fmt.Sprintf("Session not created; Reason: %s; InternalError: %v", msg, err),
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
