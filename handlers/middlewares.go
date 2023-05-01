package handlers

import (
	"net/http"
	"os"
	"strings"

	"github.com/gin-gonic/gin"
	log "github.com/sirupsen/logrus"
	"github.com/zebrunner/esg/service"
	"github.com/zebrunner/esg/utils"
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

	status := http.StatusInternalServerError
	message := "Internal server error happened. All error details collected in logs"
	var meta interface{}
	publicError := c.Errors.ByType(gin.ErrorTypePublic).Last()
	if publicError != nil {
		httpError, ok := publicError.Err.(*utils.HTTPError)
		if ok {
			status = httpError.Status
			message = httpError.Message
		}
		meta = publicError.Meta
	}
	log.WithFields(log.Fields{
		"client":   c.ClientIP(),
		"status":   status,
		"response": message,
	}).Warn("Error response response")
	c.JSON(status, utils.APIErrorResponse{
		Error:   message,
		Payload: meta,
	})
}

func SeleniumError(c *gin.Context) {
	// Add sessionID to gin context for logging purposes
	path := c.Request.URL.Path
	sessionID := ""
	if strings.HasPrefix(path, "/wd/hub/session") && len(strings.Split(path, "/")) >= 3 {
		sessionID = strings.Split(path, "/")[2]
		c.Set("sessionID", sessionID)
	}

	c.Next()
	if c.Errors.Last() == nil {
		return
	}

	for _, err := range c.Errors {
		l := log.WithError(err)
		if sess, ok := c.Get("sessionID"); ok {
			l.WithField("session", sess)
		}
		l.Debug("Selenium error received")
	}

	status := http.StatusInternalServerError
	message := "Internal server error happened. All error details collected in logs"
	seleniumCode := "unknown error"
	var meta interface{}

	publicError := c.Errors.ByType(gin.ErrorTypePublic).Last()
	if publicError != nil {
		seleniumErr, ok := publicError.Err.(*utils.SeleniumError)
		if ok {
			message = seleniumErr.Message
			seleniumCode = seleniumErr.SeleniumCode
			status = seleniumErr.ResponseStatus
		}
		meta = publicError.Meta
	}

	log.WithFields(log.Fields{
		"status":        status,
		"seleniumError": seleniumCode,
		"sessionId": sessionID,
	}).Warn("Error sent to selenium")
	c.JSON(status, gin.H{
		"value": gin.H{
			"error":   seleniumCode,
			"message": message,
			"data":    meta,
		},
	})
}

func Authentication(c *gin.Context) {
	username, password, ok := c.Request.BasicAuth()
	if !ok {

		c.JSON(http.StatusUnauthorized, utils.SeleniumError{
			ResponseStatus: http.StatusUnauthorized,
			SeleniumCode   :"Auth credentials not found",
			Message        :"Auth credentials not found",
		})
		log.WithField("client", c.ClientIP()).Warn("Auth credentials not found")
		c.Abort()
		return
	}

	err := service.CheckAuth(username, password)
	if err != nil {
		log.WithError(err).WithFields(log.Fields{
			"client":   c.ClientIP(),
			"user":     username,
			"password": password,
		}).Warn("Failed to authenticate user")
		c.JSON(http.StatusUnauthorized, utils.SeleniumError{
			ResponseStatus: http.StatusUnauthorized,
			SeleniumCode   :"Provided credentials not valid",
			Message        :"Provided credentials not valid",
		})
		c.Abort()
		return
	}
}

func APIAuthentication(c *gin.Context) {
	username, password, ok := c.Request.BasicAuth()
	if !ok {
		c.JSON(http.StatusUnauthorized,  utils.SeleniumError{
			ResponseStatus: http.StatusUnauthorized,
			SeleniumCode   :"Auth credentials not found",
			Message        :"Auth credentials not found",
		})
		log.WithField("client", c.ClientIP()).Warn("Auth credentials not found")
		c.Abort()
		return
	}

	if username != "root" || password != os.Getenv("API_ACCESS_KEY") {
		log.WithFields(log.Fields{
			"client":   c.ClientIP(),
			"user":     username,
			"password": password,
		}).Warn("Failed to authenticate user")
		c.JSON(http.StatusUnauthorized, utils.SeleniumError{
			ResponseStatus: http.StatusUnauthorized,
			SeleniumCode   : "Provided credentials not valid",
			Message        : "Provided credentials not valid",
		})
		c.Abort()
		return
	}
}
