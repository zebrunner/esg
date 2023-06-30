package handlers

import (
	"fmt"
	"os"
	"strings"

	"github.com/gin-gonic/gin"
	log "github.com/sirupsen/logrus"
	"github.com/zebrunner/esg/service"
	"github.com/zebrunner/esg/utils"
)

func APIError(c *gin.Context) {
	log.Debug("triggered APIError handler")
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
		apiErr = utils.UnknownApiErr("Internal server error happened. All error details collected in logs")
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
	log.Debug("triggered SeleniumError handler")
	path := c.Request.URL.Path
	if strings.HasPrefix(path, "/wd/hub/session") && len(strings.Split(path, "/")) >= 3 {
		sessionID := strings.Split(path, "/")[2]
		c.Set("sessionID", sessionID)
	}

	c.Next()
	if c.Errors.Last() == nil {
		return
	}

	for _, err := range c.Errors {
		l := log.WithError(err)
		if sess, ok := c.Get("sessionID"); ok {
			l.WithField("sessionId", sess)
		}
		l.Debug("Selenium error received")
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
		log.Debug("SeleniumError(): intercepted error is either not public or not Selenium Error type. Setting default values...")
		seErr = utils.UnknownErr(fmt.Errorf("internal server error happened. All error details collected in logs"))
	}

	log.WithFields(log.Fields{
		"status":  seErr.ResponseStatus,
		"error":   seErr.Name,
		"message": seErr.Err,
	}).Warn("Error sent to selenium")

	seErr.SendEncodedResponse(c)
}

func APIAuthentication(c *gin.Context) {
	log.Debug("triggered APIAuthentication handler")
	username, password, ok := c.Request.BasicAuth()
	if !ok {
		log.WithField("client", c.ClientIP()).Warn("Auth credentials not found")

		c.Error(utils.AuthApiErr("auth credentials not found")).SetType(gin.ErrorTypePublic)
		c.Abort()
		return
	}

	apiErr := service.CheckAuth(username, password)
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
	log.Debug("triggered LowLvlAuthentication handler")
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
