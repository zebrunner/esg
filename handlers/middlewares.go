package handlers

import (
	"github.com/gin-gonic/gin"
	"github.com/zebrunner/esg/utils"
	"log"
	"net/http"
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
		Error:  message,
		Payload: meta,
	})
}

func SeleniumError(c * gin.Context) {
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
			"error": "unknown error",
			"message": message,
			"data": meta,
		},
		"status": 13,
	})
	c.Abort()
}