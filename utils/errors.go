package utils

import (
	"fmt"
	"github.com/aws/aws-sdk-go/service/ecs"
	"github.com/gin-gonic/gin"
	"net/http"
)

type EsgError interface {
	Encode(ctx *gin.Context)
	Error() string
}

type HTTPError struct {
	Status  int
	Message string
}

func (err *HTTPError) Error() string {
	if err != nil {
		return fmt.Sprintf("HTTP error. Status: %v; Message: %s", err.Status, err.Message)
	}
	return "HTTP error."
}

func (err *HTTPError) Encode(ctx *gin.Context) {
	ctx.JSON(err.Status, map[string]interface{}{
		"value": map[string]string{
			"error": fmt.Sprintf("HTTP error: %s", err.Message),
		},
	})
}

func AuthNotFoundErr() *HTTPError {
	return &HTTPError{
		Status:  http.StatusBadRequest,
		Message: "Auth data not provided",
	}
}

func AuthErr() *HTTPError {
	return &HTTPError{
		Status:  http.StatusUnauthorized,
		Message: "Invalid username or password",
	}
}

func UserNotFoundErr() *HTTPError {
	return &HTTPError{
		Status:  http.StatusNotFound,
		Message: "User not found",
	}
}

func UserAlrExistsErr() *HTTPError {
	return &HTTPError{
		Status:  http.StatusBadRequest,
		Message: "User with this name already exists",
	}
}

func DeactUserAccessErr() *HTTPError {
	return &HTTPError{
		Status:  http.StatusUnauthorized,
		Message: "User deactivated, authorization not allowed",
	}
}

func InvalidReqBodyErr() *HTTPError {
	return &HTTPError{
		Status:  http.StatusBadRequest,
		Message: "Request body is invalid",
	}
}

func ParamNotFoundErr(param string) *HTTPError {
	return &HTTPError{
		Status:  http.StatusBadRequest,
		Message: fmt.Sprintf("%s parameter not found", param),
	}
}

type SeleniumError struct {
	Status  int
	Message string
	Name    string
}

func (err *SeleniumError) Error() string {
	if err != nil {
		return fmt.Sprintf("Selenium error. Status: %v; Error: %s; Message: %s", err.Status, err.Name, err.Message)
	}
	return "Selenium error."
}

func (err *SeleniumError) Encode(ctx *gin.Context) {
	ctx.JSON(err.Status, map[string]interface{}{
		"value": map[string]string{
			"error":   fmt.Sprintf("Selenium error: %s", err.Name),
			"message": err.Message,
		},
	})
}

func ResourceNotFoundErr(err error) *SeleniumError {
	return &SeleniumError{
		Status:  http.StatusNotFound,
		Message: err.Error(),
		Name:    "Resource Not Found",
	}
}

func AwsClientErr(err *ecs.ClientException) *SeleniumError {
	return &SeleniumError{
		Status:  err.StatusCode(),
		Message: err.Message(),
		Name:    err.Code(),
	}
}

func CapsProcessErr(err error) *SeleniumError {
	return &SeleniumError{
		Status:  http.StatusBadRequest,
		Message: err.Error(),
		Name:    "Capabilities process failure",
	}
}

func EnvBuildErr(err error) *SeleniumError {
	return &SeleniumError{
		Status:  http.StatusInternalServerError,
		Message: err.Error(),
		Name:    "Failed to build execution environment, session not created",
	}
}

func CreationErr(msg interface{}, err error) *SeleniumError {
	return &SeleniumError{
		Message: err.Error(),
		Status:  http.StatusInternalServerError,
		Name:    fmt.Sprintf("Session not created; Reason: %s", msg),
	}
}

func SessNotFoundErr(err error) *SeleniumError {
	return &SeleniumError{
		Status:  http.StatusNotFound,
		Message: err.Error(),
		Name:    "Invalid session id",
	}
}

func UnknownErr(err error) *SeleniumError {
	return &SeleniumError{
		Status:  http.StatusInternalServerError,
		Message: err.Error(),
		Name:    "Unknown error",
	}
}
