package handlers

import (
	"bytes"
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
	"github.com/zebrunner/esg/config"
	"github.com/zebrunner/esg/selenium"
	"github.com/zebrunner/esg/service"
	"github.com/zebrunner/esg/utils"
	"golang.org/x/net/websocket"
)

var (
	httpClient = &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
)

func startSession(ctx context.Context, sessionUrl string, header http.Header, body []byte) (map[string]interface{}, error) {
	req, err := http.NewRequest(http.MethodPost, sessionUrl, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	for key, values := range header {
		for _, value := range values {
			req.Header.Add(key, value)
		}
	}
	req = req.WithContext(ctx)
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}

	defer resp.Body.Close()
	var reply map[string]interface{}
	err = json.NewDecoder(resp.Body).Decode(&reply)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return reply, errors.New("unsuccessful response code")
	}
	return reply, nil
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

func Create(c *gin.Context) {
	remote := c.ClientIP()

	user, password, _ := c.Request.BasicAuth()
	workspace, err := service.GetWorkspace(user)
	if err != nil {
		c.Error(&utils.SeleniumError{
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
		c.Error(&utils.SeleniumError{
			SeleniumCode:   "session not created",
			ResponseStatus: http.StatusUnauthorized,
			Message:        "Session not created; Reason: Invalid username or password",
		}).SetType(gin.ErrorTypePublic)
		return
	}

	l := log.WithFields(log.Fields{
		"user":   user,
		"remote": remote,
	})

	var body selenium.RequestCaps
	err = c.BindJSON(&body)
	if err != nil {
		l.WithError(err).Error("Failed to bind json to browser struct")
		c.Error(creationError("Bad JSON format", err)).SetType(gin.ErrorTypePublic)
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
		c.Error(&processingError).SetType(gin.ErrorTypePublic)
		return
	}

	conf, err := body.GetContainerConfiguration()
	if err != nil {
		l.WithError(err).Error("Failed to get container configuration")
		c.Error(&processingError).SetType(gin.ErrorTypePublic)
		return
	}

	sessionStartTime := time.Now()
	ctx, ctxCancel := context.WithTimeout(context.Background(), config.ServiceStartupTimeout)
	defer ctxCancel()
	driver, err := service.StartDriver(ctx, *conf, workspace)
	if err == context.DeadlineExceeded {
		err = errors.New("session startup timed out")
	}
	if err != nil {
		l.WithError(err).Error("Service startup failed")
		c.Error(creationError("Failed to start browser", err)).SetType(gin.ErrorTypePublic)
		return
	}
	l.WithField("taskID", driver.TaskID).Info("Service started successfully")
	u := driver.Url

	requestBody, err := json.Marshal(body)
	if err != nil {
		l.WithError(err).Error("Failed to marshal request")
		c.Error(creationError("Failed to start browser", err)).SetType(gin.ErrorTypePublic)
		return
	}

	sessionId := ""
	c.Request.URL.Host, c.Request.URL.Path = u.Host, path.Join(u.Path, c.Request.URL.Path)
	c.Request.URL.Scheme = "http"
	l.WithFields(log.Fields{
		"serviceUrl": u,
	}).Info("Session attempted")
	resp, err := startSession(c.Request.Context(), c.Request.URL.String(), c.Request.Header, requestBody)
	if err != nil {
		l.WithError(err).Error("Session attempt failed")
		c.Error(creationError("failed to create session", err)).SetType(gin.ErrorTypePublic)
		service.RemoveTask(driver.TaskID)
		return
	}

	sessionId, err = getSessionId(resp)
	if err != nil {
		l.WithError(err).Error("Failed to get sessionId from driver response")
		c.Error(creationError("failed to create session", err)).SetType(gin.ErrorTypePublic)
		return
	}

	if sessionId == "" {
		l.WithError(err).Error("Failed to get sessionId from driver response. sessionId is empty")
		c.Error(creationError("failed to create session", err)).SetType(gin.ErrorTypePublic)
		return
	}

	sess := &selenium.Session{
		ID:        sessionId,
		Quota:     workspace,
		Caps:      body.ToMap(),
		Conf:      *conf,
		URL:       u,
		HostPort:  driver.HostPort,
		Started:   time.Now(),
		TaskID:    driver.TaskID,
		Workspace: workspace,
	}

	err = selenium.SaveSessionToCache(sess)
	if err != nil {
		l.WithError(err).Error("Session not cached")
	}
	l.WithFields(log.Fields{
		"sessionID": sessionId,
		"latency":   util.SecondsSince(sessionStartTime),
	}).Info("Session created")
	c.JSON(http.StatusOK, resp)
}

func Proxy(c *gin.Context) {
	(&httputil.ReverseProxy{
		Director: func(r *http.Request) {
			sessionID := c.Param("session")

			sess, err := selenium.CreateSessionFromCache(sessionID)
			if err != nil {
				log.WithError(err).WithField("sessionID", sessionID).Error("Cant find session")
				c.Error(err).SetType(gin.ErrorTypePublic)
				return
			}

			r.URL.Host, r.URL.Path = sess.URL.Host, path.Clean(sess.URL.Path+r.URL.Path)
			r.URL.Scheme = "http"
		},
		ErrorHandler: defaultErrorHandler(c),
	}).ServeHTTP(c.Writer, c.Request)
}

func CloseSession(c *gin.Context) {
	workspace, _, _ := c.Request.BasicAuth()
	if workspace == "" {
		workspace = "zebrunner"
	}
	sessionId := c.Param("session")
	selenium.CloseSession(workspace, sessionId)
	log.WithField("sessionID", sessionId).Info("Session closed")
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
		json.NewEncoder(w).Encode(driverError)
	}
}

func Vnc(wsconn *websocket.Conn) {
	defer wsconn.Close()
	fragments := strings.Split(wsconn.Request().Host, "/")
	vncIndex := 0
	for i, fragment := range fragments {
		if fragment == "vnc" {
			vncIndex = i
			break
		}
	}
	sid := fragments[vncIndex+1]
	l := log.WithField("sessionID", sid)
	sess, err := selenium.CreateSessionFromCache(sid)

	if err != nil {
		l.WithError(err).Error("Session not found")
		return
	}

	vncHostPort := sess.HostPort.VNC
	if vncHostPort == "" {
		l.Debug("Vnc not enabled")
		return
	}

	l.Debug("Vnc enabled")
	var d net.Dialer
	conn, err := d.DialContext(wsconn.Request().Context(), "tcp", vncHostPort)
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
	if config.TrustedMode {
		user = "zebrunner"
		ok = true
	}

	if !ok {
		c.Error(&utils.HTTPError{
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
	if config.TrustedMode {
		user = "zebrunner"
		ok = true
	}
	if !ok {
		c.Error(&utils.HTTPError{
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
		c.Error(err)
		return
	}
	c.Redirect(http.StatusFound, presignedUrl)
}

func Downloads(c *gin.Context) {
	sessionID := c.Param("session")
	filename := c.Param("file")
	sess, err := selenium.CreateSessionFromCache(sessionID)
	if err != nil {
		c.Error(err)
		return
	}

	director := func(req *http.Request) {
		req.URL.Scheme = "http"
		req.URL.Host = sess.HostPort.Fileserver
		req.Host = sess.HostPort.Fileserver
		req.URL.Path = "/" + filename
	}
	proxy := &httputil.ReverseProxy{Director: director}
	fmt.Println(c.Request)
	proxy.ServeHTTP(c.Writer, c.Request)
}

func Clipboard(c *gin.Context) {
	sessionID := c.Param("session")
	sess, err := selenium.CreateSessionFromCache(sessionID)
	if err != nil {
		c.Error(err)
		return
	}

	director := func(req *http.Request) {
		req.URL.Scheme = "http"
		req.URL.Host = sess.HostPort.Clipboard
		req.Host = sess.HostPort.Clipboard
	}
	proxy := &httputil.ReverseProxy{Director: director}
	proxy.ServeHTTP(c.Writer, c.Request)
}

func Devtools(c *gin.Context) {
	sessionID := c.Param("session")
	sess, err := selenium.CreateSessionFromCache(sessionID)
	if err != nil {
		c.Error(err)
		return
	}

	fileUrl := url.URL{
		Host: sess.HostPort.Devtools,
	}
	c.Redirect(http.StatusFound, fileUrl.String())
}
