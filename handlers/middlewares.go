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

func ErrorHandler(c *gin.Context) {
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
		l := log.WithError(err).WithField("client", c.ClientIP())
		if sess, ok := c.Get("sessionID"); ok {
			l.WithField("session", sess)
		}
		l.Debug("Error received")
	}

	publicError := c.Errors.ByType(gin.ErrorTypePublic).Last()
	if publicError != nil {
		meta := publicError.Meta
		log.WithFields(log.Fields{
			"client":    c.ClientIP(),
			"error":     publicError.Error(),
			"sessionId": sessionID,
			"meta":      meta,
		}).Warn("Error response")

		if esgErr, ok := publicError.Err.(utils.EsgError); ok {
			esgErr.Encode(c)
		} else {
			c.JSON(http.StatusInternalServerError, gin.H{
				"value": gin.H{
					"message": publicError.Error(),
					"name":    "Unknown error",
				},
			})
		}
	} else {
		log.WithFields(log.Fields{
			"client":    c.ClientIP(),
			"error":     "Internal server error happened",
			"sessionId": sessionID,
		}).Warn("Error response")
		c.JSON(http.StatusInternalServerError, gin.H{
			"value": gin.H{
				"message": "Internal server error happened. All error details collected in logs",
				"name":    "Unknown error",
			},
		})
	}
}

func Authentication(c *gin.Context) {
	username, password, ok := c.Request.BasicAuth()
	if !ok {
		_ = c.Error(utils.AuthNotFoundErr()).SetType(gin.ErrorTypePublic)
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
		_ = c.Error(err).SetType(gin.ErrorTypePublic)
		c.Abort()
		return
	}
}

func APIAuthentication(c *gin.Context) {
	username, password, ok := c.Request.BasicAuth()
	if !ok {
		log.WithField("client", c.ClientIP()).Warn("Auth credentials not found")
		_ = c.Error(utils.AuthNotFoundErr()).SetType(gin.ErrorTypePublic)
		c.Abort()
		return
	}

	if username != "root" || password != os.Getenv("API_ACCESS_KEY") {
		log.WithFields(log.Fields{
			"client":   c.ClientIP(),
			"user":     username,
			"password": password,
		}).Warn("Failed to authenticate user")
		_ = c.Error(utils.AuthErr()).SetType(gin.ErrorTypePublic)
		c.Abort()
		return
	}
}
