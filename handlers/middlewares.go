package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
	log "github.com/sirupsen/logrus"
	"github.com/zebrunner/esg/service"
	"github.com/zebrunner/esg/utils"
)

func APIError(c *gin.Context) {
	log.Info("Api error")
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
	}).Warn("Nonsuccessful response")
	c.JSON(status, utils.APIErrorResponse{
		Error:   message,
		Payload: meta,
	})
}

func SeleniumError(c *gin.Context) {
	c.Next()
	if c.Errors.Last() == nil {
		return
	}

	log.Println(c.Errors.String())
	status := http.StatusInternalServerError
	message := "Internal server error happened. All error details collected in logs"
	var meta interface{}
	publicError := c.Errors.ByType(gin.ErrorTypePublic).Last()
	if publicError != nil {
		httpError, ok := publicError.Err.(*utils.SeleniumError)
		if ok {
			message = httpError.Message
		}
		meta = publicError.Meta
	}
	c.JSON(status, gin.H{
		"value": gin.H{
			"error":   "unknown error",
			"message": message,
			"data":    meta,
		},
		"status": 13,
	})
}

func Authentication(c *gin.Context) {
	username, password, ok := c.Request.BasicAuth()
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{
			"message": "Auth credentials not found",
		})
		c.Abort()
		return
	}

	err := service.CheckAuth(username, password)
	if err != nil {
		log.Printf("Error while authentication process. %v, User: %s; Password: %s", err, username, password)
		c.JSON(http.StatusUnauthorized, gin.H{
			"message": "Provided credentials not valid",
		})
		c.Abort()
		return
	}
}
