package webserver

import (
	"encoding/json"
	"log"
	"net/http"
)

type HTTPError struct {
	Status   int
	Message  string
	Response map[string]interface{}
}

func (e *HTTPError) Error() string {
	return e.Message
}

func Reply(w http.ResponseWriter, msg map[string]interface{}, status int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(msg)
}

func JsonError(w http.ResponseWriter, e error) {
	w.Header().Set("Content-Type", "application/json")
	if httpError, ok := e.(*HTTPError); ok {
		body := map[string]interface{}{
			"status": httpError.Status,
			"value": map[string]interface{}{
				"payload": httpError.Response,
				"message": httpError.Message,
			},
		}
		Reply(w, body, httpError.Status)
	} else {
		log.Printf("[INTERNAL SERVER ERROR] [500] [%s]", e)
		body := map[string]interface{}{
			"status": http.StatusInternalServerError,
			"value": map[string]interface{}{
				"message": "Internal server error happend. Details saved in logs",
				"payload": nil,
			},
		}
		Reply(w, body, http.StatusInternalServerError)
	}
}
