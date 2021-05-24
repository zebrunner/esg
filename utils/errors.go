package utils

import (
	"fmt"
	"github.com/gin-gonic/gin"
	"log"
	"net/http"
)

type HTTPError struct {
	Status  int
	Message string
}

func (err *HTTPError) Error() string {
	return err.Message
}

type APIErrorResponse struct {
	Error   string      `json:"error"`
	Payload interface{} `json:"payload"`
}

type SeleniumError struct {
	Err string
	Message string
}

func (err *SeleniumError) Error() string {
	return fmt.Sprintf("Error: %s. Message: %s", err.Err, err.Message)
}

func APIErrorHandler(c *gin.Context) {
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
		httpError, ok := publicError.Err.(*HTTPError)
		if ok {
			status = httpError.Status
			message = httpError.Message
		}
		meta = publicError.Meta
	}
	c.JSON(status, APIErrorResponse{
		Error:  message,
		Payload: meta,
	})
}

func SeleniumErrorHandler(c * gin.Context) {
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
		httpError, ok := publicError.Err.(*SeleniumError)
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
