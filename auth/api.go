package auth

import (
	"net/http"
	"strconv"

	"github.com/aerokube/util"
	"github.com/zebrunner/esg/webserver"
)

func TenantHandler(w http.ResponseWriter, r *http.Request) {
	tenant := r.URL.Query().Get("tenant")
	if tenant == "" {
		util.JsonError(w, "tenant query parameter not found", http.StatusBadRequest)
		return
	}

	if r.Method == "POST" {
		password, err := CreateTenant(tenant)
		if err != nil {
			webserver.JsonError(w, err)
			return
		}
		response := map[string]interface{}{
			"password": password,
		}
		webserver.Reply(w, response, http.StatusOK)
	} else if r.Method == "DELETE" {
		err := DeleteTenant(tenant)
		if err != nil {
			webserver.JsonError(w, err)
			return
		}
	}
}

func RefreshTenantHandler(w http.ResponseWriter, r *http.Request) {
	tenant := r.URL.Query().Get("tenant")
	if tenant == "" {
		httpErr := webserver.HTTPError{
			Status:  http.StatusBadRequest,
			Message: "tenant query parameter not found",
		}
		webserver.JsonError(w, &httpErr)
		return
	}
	password, err := RefreshToken(tenant)
	if err != nil {
		webserver.JsonError(w, err)
		return
	}
	response := map[string]interface{}{
		"password": password,
	}
	webserver.Reply(w, response, http.StatusOK)
}

func ActivationTenantHandler(w http.ResponseWriter, r *http.Request) {
	tenant := r.URL.Query().Get("tenant")
	if tenant == "" {
		httpErr := webserver.HTTPError{
			Status:  http.StatusBadRequest,
			Message: "tenant query parameter not found",
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

	err = ActivationTenant(tenant, isActive)
	if err != nil {
		webserver.JsonError(w, err)
		return
	}
	w.WriteHeader(http.StatusOK)
}
