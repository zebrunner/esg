package utils

import (
	"fmt"
	"net/http"

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
	Err            error
}

func (seErr *SeleniumError) Error() string {
	return fmt.Sprintf("%s: %s", seErr.Name, seErr.Err.Error())
}

func (seErr *SeleniumError) SendEncodedResponse(c *gin.Context) {
	c.JSON(seErr.ResponseStatus, gin.H{
		"value": gin.H{
			"error":   seErr.Name,
			"message": seErr.Error(),
		},
	})
}

func CreationErr(err error) *SeleniumError {
	return &SeleniumError{
		ResponseStatus: http.StatusInternalServerError,
		Name:           "session not created",
		Err:            err,
	}
}

func InvalidArgErr(err error) *SeleniumError {
	return &SeleniumError{
		ResponseStatus: http.StatusBadRequest,
		Name:           "invalid argument",
		Err:            err,
	}
}

func UnknownErr(err error) *SeleniumError {
	return &SeleniumError{
		ResponseStatus: http.StatusInternalServerError,
		Name:           "unknown error",
		Err:            err,
	}
}

func NoSuchSessionErr(err error) *SeleniumError {
	return &SeleniumError{
		ResponseStatus: http.StatusNotFound,
		Name:           "invalid session id",
		Err:            err,
	}
}

func SessionStoppedErr(err error) *SeleniumError {
	return &SeleniumError{
		ResponseStatus: http.StatusForbidden,
		Name:           "session stopped",
		Err:            err,
	}
}

func NoSuchTaskErr(err error) *SeleniumError {
	return &SeleniumError{
		ResponseStatus: http.StatusNotFound,
		Name:           "invalid task id",
		Err:            err,
	}
}

func TaskStoppedErr(err error) *SeleniumError {
	return &SeleniumError{
		ResponseStatus: http.StatusForbidden,
		Name:           "task stopped",
		Err:            err,
	}
}

func AuthErr(err error) *SeleniumError {
	return &SeleniumError{
		ResponseStatus: http.StatusUnauthorized,
		Name:           "invalid credentials",
		Err:            err,
	}
}
