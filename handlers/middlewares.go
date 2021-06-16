package handlers

import (
	"log"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/zebrunner/esg/service"
	"github.com/zebrunner/esg/utils"
)

func APIError(c *gin.Context) {
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
		httpError, ok := publicError.Err.(*utils.HTTPError)
		if ok {
			status = httpError.Status
			message = httpError.Message
		}
		meta = publicError.Meta
	}
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
		"error":   "unknown error",
		"message": message,
		"data":    meta,
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
