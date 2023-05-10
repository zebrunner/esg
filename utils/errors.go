package utils

import (
	"fmt"
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
	ResponseStatus int
	SeleniumCode   string
	Message        string
	Err            error
}

func (err *SeleniumError) Error() string {
	return fmt.Sprintf("selenium error. Error: %s. Message: %s", err.Err, err.Message)
}
