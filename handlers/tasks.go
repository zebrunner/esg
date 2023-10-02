package handlers

import (
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
	"github.com/zebrunner/esg/cachemaps/sessionmap"
	"github.com/zebrunner/esg/cachemaps/taskmap"
	"github.com/zebrunner/esg/capabilities"
	"github.com/zebrunner/esg/config"
	"github.com/zebrunner/esg/db"
	"github.com/zebrunner/esg/environment"
	"github.com/zebrunner/esg/selenium"
	"github.com/zebrunner/esg/service"
	"github.com/zebrunner/esg/utils"
	"golang.org/x/net/websocket"
)

func Create(c *gin.Context) {
	remote := c.ClientIP()
	l := log.WithField("remote", remote)
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

	reqCaps, err := capabilities.ParseRequestCapabilities(c.Request.Body)
	if err != nil {
		l.WithError(err).Error("Failed to process capabilities")
		c.Error(utils.InvalidArgErr(fmt.Errorf("failed to process capabilities"), err.Error())).SetType(gin.ErrorTypePublic)
		return
	}
	log.Trace("Request capabilitites: ", reqCaps.ToMap())

	configurationCaps, err := reqCaps.GetContainerConfiguration()
	if err != nil {
		l.WithError(err).Error("Failed to process zebrunner container configuration")
		c.Error(utils.InvalidArgErr(fmt.Errorf("failed to process capabilities"), err.Error())).SetType(gin.ErrorTypePublic)
		return
	}
	log.Trace("Container configuration: ", configurationCaps)

	env, err := environment.Build(workspace, configurationCaps)
	if err != nil {
		log.WithError(err).Error("Failed to build execution environment")
		c.Error(utils.CreationErr(fmt.Errorf("failed to create executor"), err.Error())).SetType(gin.ErrorTypePublic)
		return
	}
	env.ReqCapabilities = reqCaps
	l = l.WithField("family", env.TaskDefinitionFamily).WithField(config.RouterUuid, env.RouterUUID)

	l.Info("new request")
	l.WithField("env", env).Debug("Env details")

	if strings.Contains(env.TaskDefinitionFamily, "generic") {
		_, err = service.CreateTaskDefinition(env)
		if err != nil {
			log.WithError(err).Error("Failed to create task definition")
			c.Error(utils.UnknownErr(fmt.Errorf("failed to create task defenition for generic"), err.Error())).SetType(gin.ErrorTypePublic)
			return
		}
	}

	resp, seErr := service.GetServiceStarter(env, c, l).StartService()
	if seErr != nil {
		c.Error(seErr).SetType(gin.ErrorTypePublic)
	} else {
		l.WithFields(log.Fields{"resp": resp}).Debug("Response")
		c.JSON(http.StatusOK, resp)
	}
}

func Proxy(c *gin.Context) {
	sess := c.MustGet(config.SessionIdKey).(*sessionmap.Session)
	// c.Request.URL.Path contains router UUID which should be replaced by selenium/selenoid sess.SessionID
	c.Request.URL.Path = rerouteProxy(c.Request.URL.Path, sess.SessionID)

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

func CloseSession(c *gin.Context) {
	sess := c.MustGet(config.SessionIdKey).(*sessionmap.Session)

	l := log.WithField(config.TaskIdKey, sess.TaskId)

	selenium.CloseSession(sess, sessionmap.SessionFinished)
	l = l.WithField(config.SessionIdKey, sess.SessionID)

	err := service.StopTask(sess.TaskId, taskmap.TaskFinished)
	if err != nil {
		l.WithError(err).Warn("Failed to stop task")
	}

	l.Info("task closed")
	c.JSON(http.StatusOK, gin.H{"value": nil})
}

func AbortTask(c *gin.Context) {
	task := c.MustGet(config.TaskIdKey).(*taskmap.Task)

	l := log.WithField(config.RouterUuid, task.RouterUUID).WithField(config.TaskIdKey, task.TaskId)

	if !config.Conf.SingleTenant {
		l = l.WithField("workspace", task.Workspace)
	}

	err := service.StopTask(task.TaskId, taskmap.TaskAborted)
	if err != nil {
		l.WithError(err).Warn("Failed to stop task")
	}

	l.Info("task aborted")
	c.JSON(http.StatusNoContent, gin.H{})
}

func Vnc(c *gin.Context) {
	routerUUID := c.Param("uuid")
	l := log.WithField(config.RouterUuid, routerUUID)
	l.Debug("Vnc request")

	var network environment.NetworkConfiguration
	if sess, seErr := getSession(routerUUID); seErr == nil {
		l = l.WithField(config.SessionIdKey, sess.SessionID).WithField(config.TaskIdKey, sess.TaskId)
		network = sess.Network
	} else if sess != nil {
		// session found but stopped
		l.WithError(seErr).Error("vnc error")
		c.JSON(seErr.ResponseStatus, gin.H{"error": seErr.Error()})
		return
	} else {
		task, seErr := getTask(routerUUID)
		if seErr != nil {
			l.WithError(seErr).Error("vnc error")
			c.JSON(seErr.ResponseStatus, gin.H{"error": seErr.Error()})
			return
		}
		l = l.WithField(config.TaskIdKey, task.TaskId)
		network = task.Network
	}

	vncUrl, ok := network.GetUrl("vnc")
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
	l := log.WithField("user", user).WithField(config.RouterUuid, routerUUID)
	l.Debug("Get task status")

	task, seErr := getTask(routerUUID)
	if seErr != nil {
		l.Error("Failed to get task status")
		c.Error(utils.NotFoundApiErr(seErr.Error())).SetType(gin.ErrorTypePublic)
		return
	}

	result, err := service.DescribeTask(task.TaskId)

	if err != nil {
		l.Error("Failed to get task status")
		c.Error(utils.UnknownApiErr(fmt.Sprintf("failed to get task status: %v", err.Error()))).
			SetType(gin.ErrorTypePublic)
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": result.Tasks[0].LastStatus})
}

func Downloads(c *gin.Context) {
	sess := c.MustGet(config.SessionIdKey).(*sessionmap.Session)

	director := func(req *http.Request) {
		req.URL.Scheme = "http"
		if sess != nil {
			url, _ := sess.Network.GetUrl("fileserver")
			req.URL.Host = url.Host
			req.Host = url.Host
			req.URL.Path = getRemainingPath(req.URL.Path)
		}
	}
	proxy := &httputil.ReverseProxy{Director: director}
	fmt.Println(c.Request)
	proxy.ServeHTTP(c.Writer, c.Request)
}

func Clipboard(c *gin.Context) {
	sess := c.MustGet(config.SessionIdKey).(*sessionmap.Session)

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
	sess := c.MustGet(config.SessionIdKey).(*sessionmap.Session)
	url, _ := sess.Network.GetUrl("devtools")
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
