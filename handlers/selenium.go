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
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/zebrunner/esg/utils"

	"github.com/imdario/mergo"
	"github.com/zebrunner/esg/event"
	"github.com/zebrunner/esg/service"

	"github.com/aerokube/util"
	"github.com/zebrunner/esg/session"

	//	"github.com/docker/docker/api/types"
	//	"github.com/docker/docker/pkg/stdcopy"
	"github.com/go-redis/redis/v8"
	log "github.com/sirupsen/logrus"
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
	num                   uint64
	numLock               sync.RWMutex
	RDB                   *redis.Client
	sessions              = session.NewMap()
	Timeout               time.Duration
	MaxTimeout            time.Duration
	ServiceStartupTimeout time.Duration
	SessionDeleteTimeout  time.Duration
	VideoRecorderImage    string
	manager               service.Manager
	EnableFileUpload      bool
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
	if err != nil {
		return nil, fmt.Errorf("Error happened while getting session from cache. %v", err)
	}
	s := CachedSession{}
	err = json.Unmarshal([]byte(result), &s)
	if err != nil {
		return nil, fmt.Errorf("Cant unmarshal redis data", err)
	}

	sessionTimeout, err := getSessionTimeout(s.Caps.SessionTimeout, MaxTimeout, Timeout)
	if err != nil {
		log.Println(err)
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
	log.Printf("SESSION_TIMED_OUT: Removing ECS task forcibly: '%s'!", taskId)
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
		log.Printf("[Error] Failed to create request. Error: %v", err)
		return
	}

	log.Printf("Closing session. Request: [%s %s]", req.Method, req.URL)
	resp, err := client.Do(req)
	if err != nil {
		log.Printf("[Error] Failed to cancel driver session, RequestError: %w", err)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		log.Printf("cancel request returned not success status code. Code: %d", resp.StatusCode)
		return
	}

	_, err = RDB.Del(context.Background(), sessionID).Result()
	if err != nil {
		log.Printf("[Error] Failed to delete session from redis. Error: %v", err)
		return
	}
	log.Printf("Session closed. SessionId: %s", sessionID)
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

func Create(c *gin.Context) {
	r := c.Request
	sessionStartTime := time.Now()
	user, remote := util.RequestInfo(r)
	body, err := ioutil.ReadAll(r.Body)
	r.Body.Close()
	if err != nil {
		log.Printf("[%s] [%s] [ERROR_READING_REQUEST] [%v]", user, remote, err)
		c.Error(&utils.HTTPError{
			Status:  http.StatusBadRequest,
			Message: err.Error(),
		})
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
		log.Printf("[%s] [%s] [BAD_JSON_FORMAT] [%v]", user, remote, err)
		c.Error(&utils.HTTPError{
			Status:  http.StatusBadRequest,
			Message: err.Error(),
		})
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
	var ok bool
	var sessionTimeout time.Duration
	for _, fmc := range firstMatchCaps {
		caps = browser.Caps
		mergo.Merge(&caps, *fmc)
		caps.ProcessExtensionCapabilities()
		sessionTimeout, err = getSessionTimeout(caps.SessionTimeout, MaxTimeout, Timeout)
		if err != nil {
			log.Printf("[%s] [%s] [BAD_SESSION_TIMEOUT] [%s]", user, remote, caps.SessionTimeout)
			c.Error(&utils.HTTPError{
				Status:  http.StatusBadRequest,
				Message: err.Error(),
			})
			return
		}
		resolution, err := getScreenResolution(caps.ScreenResolution)
		if err != nil {
			log.Printf("[%s] [%s] [BAD_SCREEN_RESOLUTION] [%s]", user, remote, caps.ScreenResolution)
			c.Error(&utils.HTTPError{
				Status:  http.StatusBadRequest,
				Message: err.Error(),
			})
			return
		}
		caps.ScreenResolution = resolution
		videoScreenSize, err := getVideoScreenSize(caps.VideoScreenSize, resolution)
		if err != nil {
			log.Printf("[%s] [%s] [BAD_VIDEO_SCREEN_SIZE] [%s]", user, remote, caps.VideoScreenSize)
			c.Error(&utils.HTTPError{
				Status:  http.StatusBadRequest,
				Message: err.Error(),
			})
			return
		}
		caps.VideoScreenSize = videoScreenSize
		starter, ok = manager.Find(caps)
		if ok {
			break
		}
	}
	if !ok {
		log.Printf("[%s] [%s] [ENVIRONMENT_NOT_AVAILABLE] [%s] [%s]", user, remote, caps.BrowserName(), caps.Version)
		c.Error(&utils.HTTPError{
			Status:  http.StatusBadRequest,
			Message: "Requested environment is not available",
		})
		return
	}

	// username, password, ok
	username, _, ok := r.BasicAuth()
	if !ok {
		username = service.Tenant
	}

	startedService, err := starter.StartWithCancel(username)
	if err != nil {
		log.Printf("[%s] [%s] [SERVICE_STARTUP_FAILED] [%v]", user, remote, err)
		c.Error(err)
		return
	}
	log.Printf("[%s] [%s] [SERVICE_TASK_ID] [%v]", user, remote, startedService.TaskID)
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

		log.Printf("[%s] [%s] [SESSION_ATTEMPTED] [%s] [%d]", user, remote, u.String(), i)
		//TODO: show body and capabilities in verbose mode
		resp, status := createSession(r.Context(), r.URL.String(), r.Header, body)
		select {
		case <-r.Context().Done():
			log.Printf("[%s] [%s] [CLIENT_DISCONNECTED]", user, remote)
			cancel()
			return
		default:
		}
		if status == browserStarted {
			sess, ok := resp["sessionId"].(string)
			if !ok {
				protocolError := func() {
					c.Error(&utils.HTTPError{
						Status:  http.StatusBadGateway,
						Message: "protocol error",
					})
					log.Printf("[%s] [%s] [BAD_RESPONSE]\n", user, remote)
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
				// s.ID = startedService.Container.ContainerInstanceID + startedService.Container.ID + sess
				s.ID = sess
				resp["value"].(map[string]interface{})["sessionId"] = s.ID
			} else {
				sess, ok = resp["sessionId"].(string)
				// s.ID = startedService.Container.ContainerInstanceID + startedService.Container.ID + sess
				s.ID = sess
				resp["sessionId"] = s.ID
			}
			c.JSON(http.StatusOK, resp)
			break
		} else {
			log.Printf("[%s] [%s] [SESSION_FAILED]", user, remote)
			cancel()
			return
		}
	}

	sess := &session.Session{
		Quota:    user,
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
		event.SessionStopped(event.StoppedSession{e})
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
	log.Printf("[%s] [%s] [SESSION_CREATED] [%s] [%d] [%.2fs]", user, remote, s.ID, i, util.SecondsSince(sessionStartTime))
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
			return 0, fmt.Errorf("Invalid sessionTimeout capability: %v", err)
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

			var err error = nil
			sess, ok := sessions.Get(sessionID)
			if !ok {
				sess, err = CreateSessionFromCache(sessionID)
				if err != nil {
					log.Printf("Cant find session. %v", err)
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
					log.Printf("[SESSION_DELETED] [%s]", sessionID)
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
		log.Printf("[CLIENT_DISCONNECTED] [%s] [%s] [Error: %v]", user, remote, err)
		w.WriteHeader(http.StatusBadGateway)
	}
}

func ReverseProxy(hostFn func(sess *session.Session) string, status string) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		sid, remainingPath := splitRequestPath(r.URL.Path)
		sess, ok := sessions.Get(sid)
		if ok {
			(&httputil.ReverseProxy{
				Director: func(r *http.Request) {
					r.URL.Scheme = "http"
					r.URL.Host = hostFn(sess)
					r.URL.Path = remainingPath
					log.Printf("[%s] [%s] [%s]", status, sid, remainingPath)
				},
				ErrorHandler: defaultErrorHandler(),
			}).ServeHTTP(w, r)
		} else {
			util.JsonError(w, fmt.Sprintf("Unknown session %s", sid), http.StatusNotFound)
			log.Printf("[SESSION_NOT_FOUND] [%s]", sid)
		}
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
	// return fragments[2], slash + strings.Join(fragments[3:], slash)
}

func Vnc(wsconn *websocket.Conn) {
	defer wsconn.Close()
	sid, _ := splitRequestPath(wsconn.Request().URL.Path)
	sess, err := CreateSessionFromCache(sid)
	if err == nil {
		vncHostPort := sess.HostPort.VNC
		if vncHostPort != "" {
			log.Printf("[VNC_ENABLED] [%s]", sid)
			var d net.Dialer
			conn, err := d.DialContext(wsconn.Request().Context(), "tcp", vncHostPort)
			if err != nil {
				log.Printf("[VNC_ERROR] [%v]", err)
				return
			}
			defer conn.Close()
			wsconn.PayloadType = websocket.BinaryFrame
			go func() {
				io.Copy(wsconn, conn)
				wsconn.Close()
				log.Printf("[VNC_SESSION_CLOSED] [%s]", sid)
			}()
			io.Copy(conn, wsconn)
			log.Printf("[VNC_CLIENT_DISCONNECTED] [%s]", sid)
		} else {
			log.Printf("[VNC_NOT_ENABLED] [%s]", sid)
		}
	} else {
		log.Printf("[SESSION_NOT_FOUND] [%s]", sid)
	}
}

const (
	JsonParam = "json"
)

func Logs(c *gin.Context) {
	user, _, ok := c.Request.BasicAuth()
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
