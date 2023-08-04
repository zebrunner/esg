package handlers

import (
	"errors"
	"fmt"
	"os"

	"github.com/gin-gonic/gin"
	log "github.com/sirupsen/logrus"
	"github.com/zebrunner/esg/cachemaps/sessionmap"
	"github.com/zebrunner/esg/cachemaps/taskmap"
	"github.com/zebrunner/esg/db"
	"github.com/zebrunner/esg/utils"
)

var (
	taskContextKey    = "cachedTaskKey"
	sessionContextKey = "cachedSessionKey"
)

func APIError(c *gin.Context) {
	c.Next()
	if c.Errors.Last() == nil {
		return
	}

	for _, err := range c.Errors {
		log.WithFields(log.Fields{
			"client": c.ClientIP(),
		}).WithError(err).Warn("API error received")
	}

	var apiErr *utils.APIError

	publicError := c.Errors.ByType(gin.ErrorTypePublic).Last()
	if publicError != nil {
		passedApiErr, ok := publicError.Err.(*utils.APIError)
		if ok {
			apiErr = passedApiErr
		}
	}

	if apiErr == nil {
		log.Debug("APIError(): intercepted error is either not public or not Api Error type. Setting default values...")
		apiErr = utils.UnknownApiErr("internal server error")
	}

	log.WithFields(log.Fields{
		"client":   c.ClientIP(),
		"status":   apiErr.Status,
		"response": apiErr.Message,
	}).Warn("Error response response")

	apiErr.SendEncodedResponse(c)
}

func SeleniumError(c *gin.Context) {
	// Add sessionID to gin context for logging purposes
	l := log.NewEntry(log.StandardLogger())
	sessionId := c.Param("session")
	if sessionId != "" {
		l = l.WithField("sessionId", sessionId)
		sess, seErr := getSession(sessionId)
		if seErr != nil {
			l.WithError(seErr).Error("can't access session")
			c.Error(seErr).SetType(gin.ErrorTypePublic)
			c.Abort()
		} else {
			c.Set(sessionContextKey, sess)
		}
	}

	taskId := c.Param("task")
	if taskId != "" {
		l = l.WithField("_taskId", taskId)
		task, seErr := getTask(taskId)
		if seErr != nil {
			l.WithError(seErr).Error("can't access task")
			c.Error(seErr).SetType(gin.ErrorTypePublic)
			c.Abort()
		} else {
			c.Set(taskContextKey, task)
		}
	}

	c.Next()

	if c.Errors.Last() == nil {
		return
	}

	for _, err := range c.Errors {
		l.WithError(err).Debug("Selenium error received")
	}

	var seErr *utils.SeleniumError

	publicError := c.Errors.ByType(gin.ErrorTypePublic).Last()
	if publicError != nil {
		passedSeErr, ok := publicError.Err.(*utils.SeleniumError)
		if ok {
			seErr = passedSeErr
		}
	}

	if seErr == nil {
		l.Debug("SeleniumError(): intercepted error is either not public or not Selenium Error type. Setting default values...")
		seErr = utils.UnknownErr(fmt.Errorf("internal server error"))
	}

	l.WithFields(log.Fields{
		"status":       seErr.ResponseStatus,
		"error":        seErr.Name,
		"message":      seErr.Err,
		"request path": c.Request.URL.Path,
	}).Warn("Error sent to selenium")

	seErr.SendEncodedResponse(c)
}

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

func APIAuthentication(c *gin.Context) {
	username, password, ok := c.Request.BasicAuth()
	if !ok {
		log.WithField("client", c.ClientIP()).Warn("Auth credentials not found")

		c.Error(utils.AuthApiErr("auth credentials not found")).SetType(gin.ErrorTypePublic)
		c.Abort()
		return
	}

	apiErr := db.CheckAuth(username, password)
	if apiErr != nil {
		log.WithError(apiErr).WithFields(log.Fields{
			"client":   c.ClientIP(),
			"user":     username,
			"password": password,
		}).Warn("Failed to authenticate user")

		c.Error(apiErr).SetType(gin.ErrorTypePublic)
		c.Abort()
		return
	}
}

func LowLvlAuthentication(c *gin.Context) {
	username, password, ok := c.Request.BasicAuth()
	if !ok {
		log.WithField("client", c.ClientIP()).Warn("Auth credentials not found")

		c.Error(utils.AuthApiErr("auth credentials not found")).SetType(gin.ErrorTypePublic)
		c.Abort()
		return
	}

	if username != "root" || password != os.Getenv("API_ACCESS_KEY") {
		log.WithFields(log.Fields{
			"client":   c.ClientIP(),
			"user":     username,
			"password": password,
		}).Warn("Failed to authenticate user")

		c.Error(utils.AuthApiErr("provided credentials not valid")).SetType(gin.ErrorTypePublic)
		c.Abort()
		return
	}
}
