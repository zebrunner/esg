package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/aerokube/util"
	"github.com/gin-gonic/gin"
	"github.com/go-redis/redis/v8"
	"github.com/imdario/mergo"
	log "github.com/sirupsen/logrus"
	"github.com/zebrunner/esg/event"
	"github.com/zebrunner/esg/service"
	"github.com/zebrunner/esg/session"
	"github.com/zebrunner/esg/utils"
	"golang.org/x/net/websocket"
)

const slash = "/"

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
	RDB                   *redis.Client
	sessions              = session.NewMap()
	Timeout               time.Duration
	MaxTimeout            time.Duration
	ServiceStartupTimeout time.Duration
	SessionDeleteTimeout  time.Duration
	VideoRecorderImage    string
	manager               service.Manager
	EnableFileUpload      bool
	TrustedMode           bool
)

func InitManager() {
	environment := service.Environment{
		StartupTimeout:       ServiceStartupTimeout,
		SessionDeleteTimeout: SessionDeleteTimeout,
		VideoContainerImage:  VideoRecorderImage,
	}
	manager = &service.DefaultManager{Environment: &environment}
}

type Request struct {
	*http.Request
}

type CachedSession struct {
	Quota    string
	Caps     session.Caps
	URL      *url.URL
	HostPort session.HostPort
	Timeout  time.Duration
	Started  time.Time
	TaskID   string
}

func (s CachedSession) MarshalBinary() ([]byte, error) {
	return json.Marshal(s)
}

func CreateSessionFromCache(sessionID string) (*session.Session, error) {
	result, err := RDB.Get(context.Background(), sessionID).Result()
	if err == redis.Nil {
		return nil, &utils.SeleniumError{
			ResponseStatus: http.StatusNotFound,
			SeleniumCode:   "invalid session id",
			Message:        fmt.Sprintf("Session with id %s not found in active sessions.", sessionID),
			Err:            err,
		}
	}
	if err != nil {
		return nil, err
	}
	s := CachedSession{}
	err = json.Unmarshal([]byte(result), &s)
	if err != nil {
		return nil, err
	}

	sessionTimeout, err := getSessionTimeout(s.Caps.SessionTimeout, MaxTimeout, Timeout)
	if err != nil {
		return nil, err
	}
	seleniumSession := session.Session{
		Quota:    s.Quota,
		Caps:     s.Caps,
		URL:      s.URL,
		HostPort: s.HostPort,
		Timeout:  s.Timeout,
		TimeoutCh: onTimeout(sessionTimeout, func() {
			Delete(s.TaskID)
		}),
		Started: s.Started,
		TaskID:  s.TaskID,
	}
	seleniumSession.Cancel = cancelAndRenameFiles(s.TaskID)
	return &seleniumSession, nil
}

func cancelAndRenameFiles(taskID string) func() {
	return func() {
		service.RemoveTask(taskID)
	}
}

// TODO There is simpler way to do this
func (r Request) Localaddr() string {
	addr := r.Context().Value(http.LocalAddrContextKey).(net.Addr).String()
	_, port, _ := net.SplitHostPort(addr)
	return net.JoinHostPort("127.0.0.1", port)
}

func Delete(taskId string) {
	log.WithField("taskID", taskId).Info("Session timed out. Removing ECS task forcibly")
	service.RemoveTask(taskId)
}

func CloseSession(sessionID string) {
	sess, err := CreateSessionFromCache(sessionID)
	if err != nil {
		log.WithError(err).Error("Failed to get session from cache")
		return
	}
	defer sess.Cancel()

	client := http.Client{}
	sess.URL.Path = path.Clean(sess.URL.Path + fmt.Sprintf("/session/%s", sessionID))
	timeoutCtx, cancel := context.WithTimeout(context.Background(), SessionDeleteTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(timeoutCtx, http.MethodDelete, sess.URL.String(), nil)
	if err != nil {
		log.WithError(err).Error("Failed to create request")
		return
	}

	log.WithFields(log.Fields{
		"method": req.Method,
		"url":    req.URL,
	}).Debug("Closing session.")
	resp, err := client.Do(req)
	if err != nil {
		log.WithError(err).Error("Failed to cancel driver session")
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		log.WithField("statusCode", resp.Status).Error("Cancel request returned not success status code")
		return
	}

	_, err = RDB.Del(context.Background(), sessionID).Result()
	if err != nil {
		log.WithError(err).Error("Failed to delete session from redis")
		return
	}
	log.WithField("sessionID", sessionID).Info("Session closed.")
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
	ctx, cancel := context.WithTimeout(ctx, Timeout)
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
	r := c.Request
	sessionStartTime := time.Now()
	username, password, ok := c.Request.BasicAuth()
	remote := c.ClientIP()
	if TrustedMode {
		username = "zebrunner"
		ok = true
	}
	if !ok {
		c.Error(creationError("Failed to get auth credentials.", nil)).SetType(gin.ErrorTypePublic)
		return
	}

	if !TrustedMode {
		err := service.CheckAuth(username, password)
		if err != nil {
			log.WithError(err).WithFields(log.Fields{
				"client":   c.ClientIP(),
				"user":     username,
				"password": password,
			}).Warn("Failed to authenticate user on session creation")
			c.Error(creationError("Authentication error", err)).SetType(gin.ErrorTypePublic)
			return
		}
	}

	l := log.WithFields(log.Fields{
		"user":   username,
		"remote": remote,
	})

	body, err := ioutil.ReadAll(r.Body)
	r.Body.Close()
	if err != nil {
		l.WithError(err).Error("Failed to read request")
		c.Error(creationError("Failed to read request", err)).SetType(gin.ErrorTypePublic)
		return
	}
	var browser struct {
		Caps    session.Caps `json:"desiredCapabilities"`
		W3CCaps struct {
			Caps       session.Caps    `json:"alwaysMatch"`
			FirstMatch []*session.Caps `json:"firstMatch"`
		} `json:"capabilities"`
	}
	err = json.Unmarshal(body, &browser)
	if err != nil {
		l.WithError(err).Error("Bad JSON format")
		c.Error(creationError("Bad JSON fromat", err)).SetType(gin.ErrorTypePublic)
		return
	}
	if browser.W3CCaps.Caps.BrowserName() != "" && browser.Caps.BrowserName() == "" {
		browser.Caps = browser.W3CCaps.Caps
	}
	firstMatchCaps := browser.W3CCaps.FirstMatch
	if len(firstMatchCaps) == 0 {
		firstMatchCaps = append(firstMatchCaps, &session.Caps{})
	}
	var caps session.Caps
	var starter service.Starter
	var sessionTimeout time.Duration
	for _, fmc := range firstMatchCaps {
		caps = browser.Caps
		err := mergo.Merge(&caps, *fmc)
		if err != nil {
			c.Error(err)
		}
		caps.ProcessExtensionCapabilities()
		sessionTimeout, err = getSessionTimeout(caps.SessionTimeout, MaxTimeout, Timeout)
		if err != nil {
			l.WithError(err).Error("Bas session timeout")
			c.Error(creationError("Failed to parse `sessionTimeout` capability.", err)).SetType(gin.ErrorTypePublic)
			return
		}

		resolution, err := getScreenResolution(caps.ScreenResolution)
		if err != nil {
			l.WithError(err).WithField("resolution", caps.ScreenResolution).Error("Bad screen resolution")
			c.Error(creationError("Failed to parse `resolution` capability", err)).SetType(gin.ErrorTypePublic)
			return
		}

		caps.ScreenResolution = resolution
		videoScreenSize, err := getVideoScreenSize(caps.VideoScreenSize, resolution)
		if err != nil {
			l.WithError(err).WithField("videoScreenSize", caps.VideoScreenSize).Error("Bad video screen size")
			c.Error(creationError("Failed to parse `videoScreenSize` capability", err)).SetType(gin.ErrorTypePublic)
			return
		}
		caps.VideoScreenSize = videoScreenSize
		starter, ok = manager.Find(caps)
		if ok {
			break
		}
	}
	if !ok {
		l.WithFields(log.Fields{
			"browserName":    caps.BrowserName(),
			"browserVersion": caps.Version,
		}).Error("Environment not available")
		c.Error(creationError("Requested browser not available", nil)).SetType(gin.ErrorTypePublic)
		return
	}

	startedService, err := starter.StartWithCancel(username)
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
		r.URL.Host, r.URL.Path = u.Host, path.Join(u.Path, r.URL.Path)
		r.URL.Scheme = "http"

		l.WithFields(log.Fields{
			"serviceUrl": u,
			"attempt":    i,
		}).Info("Session attempted")
		resp, status := createSession(r.Context(), r.URL.String(), r.Header, body)
		select {
		case <-r.Context().Done():
			l.Info("Client disconnected")
			cancel()
			return
		default:
		}
		if status == browserStarted {
			// sess, ok := resp["sessionId"].(string)
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
				sess, ok := valueMap["sessionId"].(string)
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

	sess := &session.Session{
		Quota:    username,
		Caps:     caps,
		URL:      u,
		HostPort: startedService.HostPort,
		Timeout:  sessionTimeout,
		TimeoutCh: onTimeout(sessionTimeout, func() {
			Delete(startedService.TaskID)
		}),
		Started: time.Now(),
	}

	cancelAndRenameFiles := func() {
		cancel()
		sessionId := s.ID
		e := event.Event{
			SessionId: sessionId,
			Session:   sess,
		}
		event.SessionStopped(event.StoppedSession{Event: e})
	}
	sess.Cancel = cancelAndRenameFiles
	sessions.Put(s.ID, sess)
	redisSession := CachedSession{
		Quota:    sess.Quota,
		Caps:     sess.Caps,
		URL:      sess.URL,
		HostPort: sess.HostPort,
		Timeout:  sess.Timeout,
		Started:  sess.Started,
		TaskID:   startedService.TaskID,
	}
	err = RDB.Set(context.Background(), s.ID, redisSession, 0).Err()
	if err != nil {
		fmt.Println("Session not cached", err)
	}
	l.WithFields(log.Fields{
		"sessionID": s.ID,
		"latency":   util.SecondsSince(sessionStartTime),
	}).Info("Session created")
}

var (
	fullFormat  = regexp.MustCompile(`^([0-9]+x[0-9]+)x(8|16|24)$`)
	shortFormat = regexp.MustCompile(`^[0-9]+x[0-9]+$`)
)

func getScreenResolution(input string) (string, error) {
	if input == "" {
		return "1920x1080x24", nil
	}
	if fullFormat.MatchString(input) {
		return input, nil
	}
	if shortFormat.MatchString(input) {
		return fmt.Sprintf("%sx24", input), nil
	}
	return "", fmt.Errorf(
		"Malformed screenResolution capability: %s. Correct format is WxH (1920x1080) or WxHxD (1920x1080x24).",
		input,
	)
}

func shortenScreenResolution(screenResolution string) string {
	return fullFormat.FindStringSubmatch(screenResolution)[1]
}

func getVideoScreenSize(videoScreenSize string, screenResolution string) (string, error) {
	if videoScreenSize != "" {
		if shortFormat.MatchString(videoScreenSize) {
			return videoScreenSize, nil
		}
		return "", fmt.Errorf(
			"Malformed videoScreenSize capability: %s. Correct format is WxH (1920x1080).",
			videoScreenSize,
		)
	}
	return shortenScreenResolution(screenResolution), nil
}

func getSessionTimeout(sessionTimeout string, maxTimeout time.Duration, defaultTimeout time.Duration) (time.Duration, error) {
	if sessionTimeout != "" {
		st, err := time.ParseDuration(sessionTimeout)
		if err != nil {
			return 0, fmt.Errorf("invalid sessionTimeout capability: %v", err)
		}
		if st <= maxTimeout {
			return st, nil
		}
		return maxTimeout, nil
	}
	return defaultTimeout, nil
}

func Proxy(c *gin.Context) {
	done := make(chan func())
	go func() {
		(<-done)()
	}()
	cancel := func() {}
	defer func() {
		done <- cancel
	}()
	(&httputil.ReverseProxy{
		Director: func(r *http.Request) {
			fragments := strings.Split(r.URL.Path, slash)
			sessionID := fragments[2]

			sess, ok := sessions.Get(sessionID)
			if !ok {
				_, err := CreateSessionFromCache(sessionID)
				if err != nil {
					log.WithError(err).WithField("sessionID", sessionID).Error("Cant find session")
					c.Error(err).SetType(gin.ErrorTypePublic)
					return
				}
			}

			if sess != nil {
				sess.Lock.Lock()
				defer sess.Lock.Unlock()
				select {
				case <-sess.TimeoutCh:
				default:
					close(sess.TimeoutCh)
				}
				if r.Method == http.MethodDelete && len(fragments) == 3 {
					if EnableFileUpload {
						os.RemoveAll(filepath.Join(os.TempDir(), sessionID))
					}
					cancel = sess.Cancel
					sessions.Remove(sessionID)
					RDB.Del(context.Background(), sessionID).Result()
					log.WithField("sessionID", sessionID).Info("Session deleted")
				} else {
					if len(fragments) == 4 && fragments[len(fragments)-1] == "file" && EnableFileUpload {
						r.Header.Set("X-Selenoid-File", filepath.Join(os.TempDir(), sessionID))
						r.URL.Path = "/file"
						return
					}
				}
				r.URL.Host, r.URL.Path = sess.URL.Host, path.Clean(sess.URL.Path+r.URL.Path)
				r.URL.Scheme = "http"
				return
			}
			r.URL.Path = "/error"
		},
		ErrorHandler: defaultErrorHandler(),
	}).ServeHTTP(c.Writer, c.Request)
}

func defaultErrorHandler() func(http.ResponseWriter, *http.Request, error) {
	return func(w http.ResponseWriter, r *http.Request, err error) {
		user, remote := util.RequestInfo(r)
		log.WithError(err).WithFields(log.Fields{
			"user":   user,
			"remote": remote,
		}).Error("Client disconnected")
		w.WriteHeader(http.StatusBadGateway)
	}
}

func splitRequestPath(p string) (string, string) {
	fragments := strings.Split(p, slash)
	vncIndex := 0
	for i, fragment := range fragments {
		if fragment == "vnc" {
			vncIndex = i
			break
		}
	}
	return fragments[vncIndex+1], slash + strings.Join(fragments[vncIndex+2:], slash)
}

func Vnc(wsconn *websocket.Conn) {
	defer wsconn.Close()
	sid, _ := splitRequestPath(wsconn.Request().URL.Path)
	l := log.WithField("sessionID", sid)
	sess, err := CreateSessionFromCache(sid)

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

const (
	JsonParam = "json"
)

func Logs(c *gin.Context) {
	user, _, ok := c.Request.BasicAuth()
	if TrustedMode {
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
	if TrustedMode {
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
	sess, err := CreateSessionFromCache(sessionID)
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
	sess, err := CreateSessionFromCache(sessionID)
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
	sess, err := CreateSessionFromCache(sessionID)
	if err != nil {
		c.Error(err)
		return
	}

	fileUrl := url.URL{
		Host: sess.HostPort.Devtools,
	}
	c.Redirect(http.StatusFound, fileUrl.String())
}

func onTimeout(t time.Duration, f func()) chan struct{} {
	cancel := make(chan struct{})
	go func(cancel chan struct{}) {
		select {
		case <-time.After(t):
			f()
		case <-cancel:
		}
	}(cancel)
	return cancel
}

func ClearSessions() {
	// TODO: Emulate session termination on selenium and try to return response
	// TODO: Move logic outside core ESG to run separately from main processes
	for {
		time.Sleep(Timeout)
		keys, err := RDB.Keys(context.Background(), "*").Result()
		if err != nil {
			log.WithError(err).Error("Failed to get list of keys")
			continue
		}

		for _, key := range keys {
			idle, err := RDB.ObjectIdleTime(context.Background(), key).Result()
			if err != nil {
				log.WithError(err).WithField("session", key).Error("Failed to get IDLE time for session.")
				continue
			}

			if idle > Timeout {
				result, err := RDB.Get(context.Background(), key).Result()
				if err != nil {
					log.WithError(err).Error("Failed to get session from cache")
					continue
				}
				s := CachedSession{}
				err = json.Unmarshal([]byte(result), &s)
				if err != nil {
					log.WithError(err).Error("Failed to unmarshal redis response")
					continue
				}
				log.WithField("task", s.TaskID).Info("Deleting task. Reson: idle temeout")
				CloseSession(key)
				_, err = RDB.Del(context.Background(), key).Result()
				if err != nil {
					log.WithError(err).WithField("session", key).Error("Failed to delete session from cache")
				}
			}
		}
	}
}
