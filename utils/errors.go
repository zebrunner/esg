package utils

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

type APIError struct {
	Status  int
	Message string
}

func (apiErr *APIError) Error() string {
	return apiErr.Message
}

func (apiErr *APIError) SendEncodedResponse(c *gin.Context) {
	c.JSON(apiErr.Status, gin.H{
		"error": apiErr.Message,
	})
}

func UnknownApiErr(message string) *APIError {
	return &APIError{
		Status:  http.StatusInternalServerError,
		Message: message,
	}
}

func AuthApiErr(message string) *APIError {
	return &APIError{
		Status:  http.StatusUnauthorized,
		Message: message,
	}
}

func InvalidApiRequestErr(message string) *APIError {
	return &APIError{
		Status:  http.StatusBadRequest,
		Message: fmt.Sprintf("invalid request body: %v", message),
	}
}

func NotFoundApiErr(message string) *APIError {
	return &APIError{
		Status:  http.StatusNotFound,
		Message: message,
	}
}

type SeleniumError struct {
	ResponseStatus int
	Name           string
	MainErr        error
	DebugInfo      []string
}

func (seErr *SeleniumError) Error() string {
	return fmt.Sprintf("%s: %s. %s", seErr.Name, seErr.MainErr.Error(), strings.Join(seErr.DebugInfo, "."))
}

func (seErr *SeleniumError) SendEncodedResponse(c *gin.Context, enableDebug bool) {
	var message string
	if enableDebug {
		message = fmt.Sprintf("%s. %s", seErr.MainErr, strings.Join(seErr.DebugInfo, ". "))
	} else {
		message = seErr.MainErr.Error()
	}

	c.JSON(seErr.ResponseStatus, gin.H{
		"value": gin.H{
			"error":   seErr.Name,
			"message": message,
		},
	})
}

func CreationErr(err error, extraInfo ...string) *SeleniumError {
	return &SeleniumError{
		ResponseStatus: http.StatusInternalServerError,
		Name:           "session not created",
		MainErr:        err,
		DebugInfo:      extraInfo,
	}
}

func InvalidArgErr(err error, extraInfo ...string) *SeleniumError {
	return &SeleniumError{
		ResponseStatus: http.StatusBadRequest,
		Name:           "invalid argument",
		MainErr:        err,
		DebugInfo:      extraInfo,
	}
}

func UnknownErr(err error, extraInfo ...string) *SeleniumError {
	return &SeleniumError{
		ResponseStatus: http.StatusInternalServerError,
		Name:           "unknown error",
		MainErr:        err,
		DebugInfo:      extraInfo,
	}
}

func NoSuchSessionErr(err error, extraInfo ...string) *SeleniumError {
	return &SeleniumError{
		ResponseStatus: http.StatusNotFound,
		Name:           "invalid session id",
		MainErr:        err,
		DebugInfo:      extraInfo,
	}
}

func SessionStoppedErr(err error, extraInfo ...string) *SeleniumError {
	return &SeleniumError{
		ResponseStatus: http.StatusForbidden,
		Name:           "session stopped",
		MainErr:        err,
		DebugInfo:      extraInfo,
	}
}

func NoSuchTaskErr(err error, extraInfo ...string) *SeleniumError {
	return &SeleniumError{
		ResponseStatus: http.StatusNotFound,
		Name:           "invalid task id",
		MainErr:        err,
		DebugInfo:      extraInfo,
	}
}

func TaskStoppedErr(err error, extraInfo ...string) *SeleniumError {
	return &SeleniumError{
		ResponseStatus: http.StatusForbidden,
		Name:           "task stopped",
		MainErr:        err,
		DebugInfo:      extraInfo,
	}
}

func AuthErr(err error, extraInfo ...string) *SeleniumError {
	return &SeleniumError{
		ResponseStatus: http.StatusUnauthorized,
		Name:           "invalid credentials",
		MainErr:        err,
		DebugInfo:      extraInfo,
	}
}
