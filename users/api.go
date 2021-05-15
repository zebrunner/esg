package users

import (
	"net/http"
	"strconv"

	"github.com/aerokube/util"
	"github.com/zebrunner/esg/webserver"
)

func UserHandler(w http.ResponseWriter, r *http.Request) {
	username := r.URL.Query().Get("username")
	if username == "" {
		util.JsonError(w, "username query parameter not found", http.StatusBadRequest)
		return
	}

	if r.Method == "POST" {
		password, err := CreateUser(username)
		if err != nil {
			webserver.JsonError(w, err)
			return
		}
		response := map[string]interface{}{
			"access-token": password,
		}
		webserver.Reply(w, response, http.StatusOK)
	} else if r.Method == "DELETE" {
		err := DeleteUser(username)
		if err != nil {
			webserver.JsonError(w, err)
			return
		}
	}
}

func RefreshUserHandler(w http.ResponseWriter, r *http.Request) {
	user := r.URL.Query().Get("username")
	if user == "" {
		httpErr := webserver.HTTPError{
			Status:  http.StatusBadRequest,
			Message: "username query parameter not found",
		}
		webserver.JsonError(w, &httpErr)
		return
	}
	password, err := RefreshToken(user)
	if err != nil {
		webserver.JsonError(w, err)
		return
	}
	response := map[string]interface{}{
		"access-token": password,
	}
	webserver.Reply(w, response, http.StatusOK)
}

func ActivationUserHandler(w http.ResponseWriter, r *http.Request) {
	user := r.URL.Query().Get("username")
	if user == "" {
		httpErr := webserver.HTTPError{
			Status:  http.StatusBadRequest,
			Message: "username query parameter not found",
		}
		webserver.JsonError(w, &httpErr)
		return
	}
	isActive, err := strconv.ParseBool(r.URL.Query().Get("is_active"))
	if err != nil {
		httpErr := webserver.HTTPError{
			Status:  http.StatusBadRequest,
			Message: "is_active query parameter not found or has invalid format",
		}
		webserver.JsonError(w, &httpErr)
		return
	}

	err = ActivationUser(user, isActive)
	if err != nil {
		webserver.JsonError(w, err)
		return
	}
	w.WriteHeader(http.StatusOK)
}
