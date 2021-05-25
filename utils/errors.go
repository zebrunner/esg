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
	Err string
	Message string
}

func (err *SeleniumError) Error() string {
	return fmt.Sprintf("Error: %s. Message: %s", err.Err, err.Message)
}
