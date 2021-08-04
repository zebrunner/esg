package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/ioutil"

	// "io/ioutil"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/aerokube/util"
	"github.com/gin-gonic/gin"
	"github.com/imdario/mergo"
	log "github.com/sirupsen/logrus"
	"github.com/zebrunner/esg/config"
	"github.com/zebrunner/esg/selenium"
	"github.com/zebrunner/esg/service"
	"github.com/zebrunner/esg/utils"

	// "github.com/zebrunner/esg/zebrunner"
	"golang.org/x/net/websocket"
)

const (
	browserStarted = iota
	browserFailed
	seleniumError
)

var (
	httpClient = &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	manager service.Manager
)

func InitManager() {
	environment := service.Environment{
		StartupTimeout:       config.ServiceStartupTimeout,
		SessionDeleteTimeout: config.SessionDeleteTimeout,
		VideoContainerImage:  config.VideoRecorderImage,
	}
	manager = &service.DefaultManager{Environment: &environment}
}

// create() method from ggr.
func createSession(ctx context.Context, sessionUrl string, header http.Header, body []byte) (map[string]interface{}, int) {
	req, err := http.NewRequest(http.MethodPost, sessionUrl, bytes.NewReader(body))
	if err != nil {
		return nil, seleniumError
	}
	for key, values := range header {
		for _, value := range values {
			req.Header.Add(key, value)
		}
	}
	req.Header.Del("Accept-Encoding")
	ctx, cancel := context.WithTimeout(ctx, config.Timeout)
	defer cancel()
	req = req.WithContext(ctx)
	req.Header.Set("Content-Type", "application/json")
	resp, err := httpClient.Do(req)
	if resp != nil {
		defer resp.Body.Close()
	}
	if err != nil {
		return nil, seleniumError
	}
	location := resp.Header.Get("Location")
	if location != "" {
		l, err := url.Parse(location)
		if err != nil {
			return nil, seleniumError
		}
		fragments := strings.Split(l.Path, "/")
		return map[string]interface{}{"sessionId": fragments[len(fragments)-1], "status": 0, "value": struct{}{}}, browserStarted
	}
	var reply map[string]interface{}
	err = json.NewDecoder(resp.Body).Decode(&reply)
	if err != nil {
		return nil, seleniumError
	}
	if resp.StatusCode != http.StatusOK {
		return reply, browserFailed
	}
	return reply, browserStarted
}

func creationError(msg string, err error) *utils.SeleniumError {
	return &utils.SeleniumError{
		SeleniumCode:   "session not created",
		ResponseStatus: http.StatusInternalServerError,
		Message:        fmt.Sprintf("Session not created; Reason: %s; InternalError: %v", msg, err),
	}
}

func Create(c *gin.Context) {
	sessionStartTime := time.Now()
	username, password, ok := c.Request.BasicAuth()
	remote := c.ClientIP()

	// Authentication related logic
	if config.TrustedMode {
		username = "zebrunner"
		ok = true
	}
	if !ok {
		c.Error(&utils.SeleniumError{
			SeleniumCode:   "session not created",
			ResponseStatus: http.StatusUnauthorized,
			Message:        "Session not created; Reason: Failed to get auth credentials.",
		}).SetType(gin.ErrorTypePublic)
		return
	}

	if !config.TrustedMode {
		err := service.CheckAuth(username, password)
		if err != nil {
			log.WithError(err).WithFields(log.Fields{
				"client":   c.ClientIP(),
				"user":     username,
				"password": password,
			}).Warn("Failed to authenticate user on session creation")
			c.Error(&utils.SeleniumError{
				SeleniumCode:   "session not created",
				ResponseStatus: http.StatusUnauthorized,
				Message:        "Session not created; Reason: Invalid username or password",
			}).SetType(gin.ErrorTypePublic)
			return
		}
	}

	l := log.WithFields(log.Fields{
		"user":   username,
		"remote": remote,
	})

	// Capability processing/validation
	//body, err := ioutil.ReadAll(c.Request.Body)
	//defer c.Request.Body.Close()
	//if err != nil {
	//	l.WithError(err).Error("Failed to read request")
	//	c.Error(creationError("Failed to read request", err)).SetType(gin.ErrorTypePublic)
	//	return
	//}

	requestCapabilities := selenium.RequestCaps{}
	err := c.BindJSON(&requestCapabilities)
	if err != nil {
		l.WithError(err).Error("Failed to bind json to browser struct")
		c.Error(creationError("Bad JSON format", err)).SetType(gin.ErrorTypePublic)
		return
	}

	sessionCaps, err := requestCapabilities.ProcessRequestCaps()
	if err != nil {
		// TODO: Return some error for a client
		return
	}
	fmt.Println(sessionCaps)

	//if requestCapabilities.Capabilities.Caps.BrowserName() != "" && requestCapabilities.Capabilities.Caps.BrowserName() == "" {
	//	requestCapabilities.DesiredCapabilities = requestCapabilities.Capabilities.Caps
	//}
	//firstMatchCaps := requestCapabilities.Capabilities.FirstMatch
	//if len(firstMatchCaps) == 0 {
	//	firstMatchCaps = append(firstMatchCaps, &selenium.Caps{})
	//}
	//var caps selenium.Caps
	//var starter service.Starter
	//for _, fmc := range firstMatchCaps {
	//	caps = requestCapabilities.DesiredCapabilities
	//	err := mergo.Merge(&caps, *fmc)
	//	if err != nil {
	//		c.Error(err)
	//	}
	//	//caps.ProcessExtensionCapabilities()
	//	if err != nil {
	//		l.WithError(err).Error("Bas session timeout")
	//		c.Error(creationError("Failed to parse `sessionTimeout` capability.", err)).SetType(gin.ErrorTypePublic)
	//		return
	//	}
	//
	//	resolution, err := caps.GetScreenResolution()
	//	if err != nil {
	//		l.WithError(err).WithField("resolution", caps.ScreenResolution).Error("Bad screen resolution")
	//		c.Error(creationError("Failed to parse `resolution` capability", err)).SetType(gin.ErrorTypePublic)
	//		return
	//	}
	//
	//	caps.ScreenResolution = resolution
	//	videoScreenSize, err := caps.GetVideoScreenSize()
	//	if err != nil {
	//		l.WithError(err).WithField("videoScreenSize", caps.VideoScreenSize).Error("Bad video screen size")
	//		c.Error(creationError("Failed to parse `videoScreenSize` capability", err)).SetType(gin.ErrorTypePublic)
	//		return
	//	}
	starter, ok := manager.Find(sessionCaps)
	//}
	//if !ok {
	//	l.WithFields(log.Fields{
	//		"browserName":    caps.BrowserName(),
	//		"browserVersion": caps.Version,
	//	}).Error("Environment not available")
	//	c.Error(creationError("Requested browser not available", nil)).SetType(gin.ErrorTypePublic)
	//	return
	//}

	ctx, ctxCancel := context.WithTimeout(context.Background(), config.ServiceStartupTimeout)
	defer ctxCancel()
	startedService, err := starter.StartWithCancel(ctx, username)
	if err == context.DeadlineExceeded {
		err = errors.New("session startup timed out")
	}
	if err != nil {
		l.WithError(err).Error("Service startup failed")
		c.Error(creationError("Failed to start browser", err)).SetType(gin.ErrorTypePublic)
		return
	}
	l.WithField("taskID", startedService.TaskID).Info("Service started successfully")
	u := startedService.Url
	cancel := startedService.Cancel
	i := 1

	var s struct {
		Value struct {
			ID string `json:"sessionId"`
		}
		ID string `json:"sessionId"`
	}
	for ; ; i++ {
		c.Request.URL.Host, c.Request.URL.Path = u.Host, path.Join(u.Path, c.Request.URL.Path)
		c.Request.URL.Scheme = "http"

		l.WithFields(log.Fields{
			"serviceUrl": u,
			"attempt":    i,
		}).Info("Session attempted")
		resp, status := createSession(c.Request.Context(), c.Request.URL.String(), c.Request.Header, body)
		select {
		case <-c.Request.Context().Done():
			l.Info("Client disconnected")
			cancel()
			return
		default:
		}
		if status == browserStarted {
			sess, ok := resp["sessionId"].(string)
			if !ok {
				protocolError := func() {
					l.Error("Bad response")
					c.Error(creationError("Protocol error", nil))
				}
				value, ok := resp["value"]
				if !ok {
					protocolError()
					cancel()
					return
				}
				valueMap, ok := value.(map[string]interface{})
				if !ok {
					protocolError()
					cancel()
					return
				}
				sess, ok = valueMap["sessionId"].(string)
				if !ok {
					protocolError()
					cancel()
					return
				}
				s.ID = sess
				resp["value"].(map[string]interface{})["sessionId"] = s.ID
			} else {
				sess, _ := resp["sessionId"].(string)
				s.ID = sess
				resp["sessionId"] = s.ID
			}
			c.JSON(http.StatusOK, resp)
			break
		} else {
			l.Warn("Session failed")
			cancel()
			return
		}
	}

	sess := &selenium.Session{
		Quota:    username,
		Caps:     sessionCaps,
		URL:      u,
		HostPort: startedService.HostPort,
		Started:  time.Now(),
	}

	redisSession := selenium.CachedSession{
		Quota:     sess.Quota,
		Caps:      sess.Caps,
		URL:       sess.URL,
		HostPort:  sess.HostPort,
		Started:   sess.Started,
		TaskID:    startedService.TaskID,
		Workspace: username,
	}
	err = config.RedisConnection.Set(context.Background(), s.ID, redisSession, 0).Err()
	if err != nil {
		fmt.Println("Session not cached", err)
	}
	l.WithFields(log.Fields{
		"sessionID": s.ID,
		"latency":   util.SecondsSince(sessionStartTime),
	}).Info("Session created")
}

func Proxy(c *gin.Context) {
	(&httputil.ReverseProxy{
		Director: func(r *http.Request) {
			fragments := strings.Split(r.URL.Path, "/")
			sessionID := fragments[2]

			workspace, _, _ := c.Request.BasicAuth()
			if workspace == "" {
				workspace = "zebrunner"
			}

			sess, err := selenium.CreateSessionFromCache(sessionID)
			if err != nil {
				log.WithError(err).WithField("sessionID", sessionID).Error("Cant find session")
				c.Error(err).SetType(gin.ErrorTypePublic)
				return
			}

			if r.Method == http.MethodDelete && len(fragments) == 3 {
				selenium.CloseSession(workspace, sessionID)
				log.WithField("sessionID", sessionID).Info("Session deleted")
			} else {
				if len(fragments) == 4 && fragments[len(fragments)-1] == "file" && config.EnableFileUpload {
					r.Header.Set("X-Selenoid-File", filepath.Join(os.TempDir(), sessionID))
					r.URL.Path = "/file"
					return
				}
			}
			r.URL.Host, r.URL.Path = sess.URL.Host, path.Clean(sess.URL.Path+r.URL.Path)
			r.URL.Scheme = "http"
		},
		ErrorHandler: defaultErrorHandler(c),
	}).ServeHTTP(c.Writer, c.Request)
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
