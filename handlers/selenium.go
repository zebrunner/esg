package handlers

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/gin-gonic/gin"
	"github.com/zebrunner/esg/utils"
	"io"
	"io/ioutil"
	"log"
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

	"github.com/imdario/mergo"
	"github.com/zebrunner/esg/event"
	"github.com/zebrunner/esg/service"

	"github.com/aerokube/util"
	"github.com/zebrunner/esg/session"

	//	"github.com/docker/docker/api/types"
	//	"github.com/docker/docker/pkg/stdcopy"
	"github.com/go-redis/redis/v8"
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
	SaveAllLogs           bool
	LogOutputDir          string
	ServiceStartupTimeout time.Duration
	SessionDeleteTimeout  time.Duration
	CaptureDriverLogs     bool
	VideoOutputDir        string
	VideoRecorderImage    string
	manager               service.Manager
	EnableFileUpload      bool
)

func InitManager() {
	environment := service.Environment{
		StartupTimeout:       ServiceStartupTimeout,
		SessionDeleteTimeout: SessionDeleteTimeout,
		CaptureDriverLogs:    CaptureDriverLogs,
		VideoOutputDir:       VideoOutputDir,
		VideoContainerImage:  VideoRecorderImage,
		LogOutputDir:         LogOutputDir,
		SaveAllLogs:          SaveAllLogs,
	}
	manager = &service.DefaultManager{Environment: &environment}
}

type Request struct {
	*http.Request
}

type sess struct {
	addr string
	id   string
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

func CreateSessionFromCache(sessionID string, r *http.Request) (*session.Session, error) {
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
			Request{r}.session(sessionID).Delete(s.TaskID)
		}),
		Started: s.Started,
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

func (r Request) session(id string) *sess {
	return &sess{r.Localaddr(), id}
}

func (s *sess) url() string {
	return fmt.Sprintf("http://%s/wd/hub/session/%s", s.addr, s.id)
}

func (s *sess) Delete(taskId string) {
	log.Printf("SESSION_TIMED_OUT: Removing ECS task forcibly: '%s' for sessionId: '%s'!", taskId, s.id)
	service.RemoveTask(taskId)
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

//func errMsg(msg string) map[string]interface{} {
//	return map[string]interface{}{
//		"value": map[string]string{
//			"message": msg,
//		},
//		"status": 13,
//	}
//}

func serial() uint64 {
	numLock.Lock()
	defer numLock.Unlock()
	id := num
	num++
	return id
}

func getSerial() uint64 {
	numLock.RLock()
	defer numLock.RUnlock()
	return num
}

func Create(c *gin.Context) {
	r := c.Request
	sessionStartTime := time.Now()
	requestId := serial()
	user, remote := util.RequestInfo(r)
	body, err := ioutil.ReadAll(r.Body)
	r.Body.Close()
	if err != nil {
		log.Printf("[%d] [%s] [%s] [ERROR_READING_REQUEST] [%v]", requestId, user, remote, err)
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
		log.Printf("[%d] [%s] [%s] [BAD_JSON_FORMAT] [%v]", requestId, user, remote, err)
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
	var finalLogName string
	for _, fmc := range firstMatchCaps {
		caps = browser.Caps
		mergo.Merge(&caps, *fmc)
		caps.ProcessExtensionCapabilities()
		sessionTimeout, err = getSessionTimeout(caps.SessionTimeout, MaxTimeout, Timeout)
		if err != nil {
			log.Printf("[%d] [%s] [%s] [BAD_SESSION_TIMEOUT] [%s]", requestId, user, remote, caps.SessionTimeout)
			c.Error(&utils.HTTPError{
				Status:  http.StatusBadRequest,
				Message: err.Error(),
			})
			return
		}
		resolution, err := getScreenResolution(caps.ScreenResolution)
		if err != nil {
			log.Printf("[%d] [%s] [%s] [BAD_SCREEN_RESOLUTION] [%s]", requestId, user, remote, caps.ScreenResolution)
			c.Error(&utils.HTTPError{
				Status:  http.StatusBadRequest,
				Message: err.Error(),
			})
			return
		}
		caps.ScreenResolution = resolution
		videoScreenSize, err := getVideoScreenSize(caps.VideoScreenSize, resolution)
		if err != nil {
			log.Printf("[%d] [%s] [%s] [BAD_VIDEO_SCREEN_SIZE] [%s]", requestId, user, remote, caps.VideoScreenSize)
			c.Error(&utils.HTTPError{
				Status:  http.StatusBadRequest,
				Message: err.Error(),
			})
			return
		}
		caps.VideoScreenSize = videoScreenSize
		finalLogName = caps.LogName
		if LogOutputDir != "" && (SaveAllLogs || caps.Log) {
			caps.LogName = getTemporaryFileName(LogOutputDir, logFileExtension)
		}
		starter, ok = manager.Find(caps, requestId)
		if ok {
			break
		}
	}
	if !ok {
		log.Printf("[%d] [%s] [%s] [ENVIRONMENT_NOT_AVAILABLE] [%s] [%s]", requestId, user, remote, caps.BrowserName(), caps.Version)
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
		log.Printf("[%d] [%s] [%s] [SERVICE_STARTUP_FAILED] [%v]", requestId, user, remote, err)
		c.Error(err)
		return
	}
	log.Printf("[%d] [%s] [%s] [SERVICE_TASK_ID] [%v]", requestId, user, remote, startedService.TaskID)
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

		log.Printf("[%d] [%s] [%s] [SESSION_ATTEMPTED] [%s] [%d]", requestId, user, remote, u.String(), i)
		//TODO: show body and capabilities in verbose mode
		//TODO: implement response updater to populate task id as part of sessionId
		resp, status := createSession(r.Context(), r.URL.String(), r.Header, body)
		select {
		case <-r.Context().Done():
			log.Printf("[%d] {%s] [%s] [CLIENT_DISCONNECTED]", requestId, user, remote)
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
					log.Printf("[%d] [%s] [%s] [BAD_RESPONSE]\n", requestId, user, remote)
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
			log.Printf("[%d] [%s] [%s] [SESSION_FAILED]", requestId, user, remote)
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
			Request{r}.session(s.ID).Delete(startedService.TaskID)
		}),
		Started: time.Now(),
	}

	cancelAndRenameFiles := func() {
		cancel()
		sessionId := s.ID
		e := event.Event{
			RequestId: requestId,
			SessionId: sessionId,
			Session:   sess,
		}
		if LogOutputDir != "" && (SaveAllLogs || caps.Log) {
			//The following logic will fail if -capture-driver-logs is enabled and a session is requested in driver mode.
			//Specifying both -log-output-dir and -capture-driver-logs in that case is considered a misconfiguration.
			oldLogName := filepath.Join(LogOutputDir, caps.LogName)
			if finalLogName == "" {
				finalLogName = sessionId + logFileExtension
				e.Session.Caps.LogName = finalLogName
			}
			newLogName := filepath.Join(LogOutputDir, finalLogName)
			err := os.Rename(oldLogName, newLogName)
			if err != nil {
				log.Printf("[%d] [LOG_ERROR] [%s]", requestId, fmt.Sprintf("Failed to rename %s to %s: %v", oldLogName, newLogName, err))
			} else {
				createdFile := event.CreatedFile{
					Event: e,
					Name:  newLogName,
					Type:  "log",
				}
				event.FileCreated(createdFile)
			}
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
	log.Printf("[%d] [%s] [%s] [SESSION_CREATED] [%s] [%d] [%.2fs]", requestId, user, remote, s.ID, i, util.SecondsSince(sessionStartTime))
}

const (
	videoFileExtension = ".mp4"
	logFileExtension   = ".log"
)

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

func getTemporaryFileName(dir string, extension string) string {
	filename := ""
	for {
		filename = generateRandomFileName(extension)
		_, err := os.Stat(filepath.Join(dir, filename))
		if err != nil {
			break
		}
	}
	return filename
}

func generateRandomFileName(extension string) string {
	randBytes := make([]byte, 16)
	rand.Read(randBytes)
	return "selenoid" + hex.EncodeToString(randBytes) + extension
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

			//TODO: candidate to hide on verbose log level
			log.Printf("[PROXY_TO] [%s]", r.URL.Path)
			var err error = nil
			sess, ok := sessions.Get(sessionID)
			if !ok {
				sess, err = CreateSessionFromCache(sessionID, r)
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
					//					sess.TimeoutCh = onTimeout(sess.Timeout, func() {
					//						request{r}.session(sessionID).Delete(sess.TaskID)
					//					})
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
			//r.URL.Path = paths.Error
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
					log.Printf("[%d] [%s] [%s] [%s]", status, sid, remainingPath)
				},
				ErrorHandler: defaultErrorHandler(),
			}).ServeHTTP(w, r)
		} else {
			util.JsonError(w, fmt.Sprintf("Unknown session %s", sid), http.StatusNotFound)
			log.Printf("[%d] [SESSION_NOT_FOUND] [%s]", sid)
		}
	}
}

func splitRequestPath(p string) (string, string) {
	fragments := strings.Split(p, slash)
	return fragments[2], slash + strings.Join(fragments[3:], slash)
}

func File(c *gin.Context) {
	var body struct {
		File []byte `json:"file" binding:"required"`
	}
	err := c.ShouldBindJSON(&body)
	if err != nil {
		c.Error(err).SetType(gin.ErrorTypePublic)
		return
	}
	z, err := zip.NewReader(bytes.NewReader(body.File), int64(len(body.File)))
	if err != nil {
		c.Error(err).SetType(gin.ErrorTypePublic)
		return
	}
	if len(z.File) != 1 {
		c.Error(&utils.HTTPError{
			Status: http.StatusBadRequest,
			Message: fmt.Sprintf("Expected there to be only 1 file. There were: %d", len(z.File)),
		}).SetType(gin.ErrorTypePublic)
		return
	}
	file := z.File[0]
	src, err := file.Open()
	if err != nil {
		c.Error(&utils.HTTPError{
			Status: http.StatusBadRequest,
			Message: err.Error(),
		}).SetType(gin.ErrorTypePublic)
		return
	}
	defer src.Close()

	dir := c.GetHeader("X-Selenoid-File")
	err = os.MkdirAll(dir, 0755)
	if err != nil {
		c.Error(err)
		return
	}
	fileName := filepath.Join(dir, file.Name)
	dst, err := os.OpenFile(fileName, os.O_WRONLY|os.O_CREATE, 0644)
	if err != nil {
		c.Error(err)
		return
	}
	defer dst.Close()
	_, err = io.Copy(dst, src)
	if err != nil {
		c.Error(err)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"value": fileName,
	})
}

func Vnc(wsconn *websocket.Conn) {
	defer wsconn.Close()
	requestId := serial()
	sid, _ := splitRequestPath(wsconn.Request().URL.Path)
	sess, ok := sessions.Get(sid)
	if ok {
		vncHostPort := sess.HostPort.VNC
		if vncHostPort != "" {
			log.Printf("[%d] [VNC_ENABLED] [%s]", requestId, sid)
			var d net.Dialer
			conn, err := d.DialContext(wsconn.Request().Context(), "tcp", vncHostPort)
			if err != nil {
				log.Printf("[%d] [VNC_ERROR] [%v]", requestId, err)
				return
			}
			defer conn.Close()
			wsconn.PayloadType = websocket.BinaryFrame
			go func() {
				io.Copy(wsconn, conn)
				wsconn.Close()
				log.Printf("[%d] [VNC_SESSION_CLOSED] [%s]", requestId, sid)
			}()
			io.Copy(conn, wsconn)
			log.Printf("[%d] [VNC_CLIENT_DISCONNECTED] [%s]", requestId, sid)
		} else {
			log.Printf("[%d] [VNC_NOT_ENABLED] [%s]", requestId, sid)
		}
	} else {
		log.Printf("[%d] [SESSION_NOT_FOUND] [%s]", requestId, sid)
	}
}

const (
	JsonParam = "json"
)

func Logs(c *gin.Context) {
	user, _, ok := c.Request.BasicAuth()
	if !ok {
		c.Error(&utils.HTTPError{
			Status: http.StatusBadRequest,
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
			Status: http.StatusBadRequest,
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

func ListFilesAsJson(w http.ResponseWriter, dir string, errStatus string) {
	files, err := ioutil.ReadDir(dir)
	if err != nil {
		log.Printf("[%s] [%s]", errStatus, fmt.Sprintf("Failed to list directory %s: %v", LogOutputDir, err))
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	var ret []string
	for _, f := range files {
		ret = append(ret, f.Name())
	}
	w.Header().Add("Content-Type", "application/json")
	json.NewEncoder(w).Encode(ret)
}

func deleteFileIfExists(w http.ResponseWriter, r *http.Request, dir string, prefix string, status string) {
	user, remote := util.RequestInfo(r)
	fileName := strings.TrimPrefix(r.URL.Path, prefix)
	filePath := filepath.Join(dir, fileName)
	_, err := os.Stat(filePath)
	if err != nil {
		http.Error(w, fmt.Sprintf("Unknown file %s", filePath), http.StatusNotFound)
		return
	}
	err = os.Remove(filePath)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to delete file %s: %v", filePath, err), http.StatusInternalServerError)
		return
	}
	log.Printf("[%s] [%s] [%s] [%s]", status, user, remote, fileName)
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
