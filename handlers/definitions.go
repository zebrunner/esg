package handlers

import (
	"context"
	"io"
	"net/http"

	"github.com/gin-gonic/gin"
	log "github.com/sirupsen/logrus"
	"github.com/zebrunner/esg/config"
	"github.com/zebrunner/esg/images"
	"github.com/zebrunner/esg/task-definitions-service/definitions"
)

var (
	DefinitionRefreshDone = false
)

type imageDataModel struct {
	Name           string `json:"name"`
	Version        string `json:"version"`
	Platform       string `json:"platform"`
	BrowserName    string `json:"browserName,omitempty"`
	BrowserVersion string `json:"browserVersion,omitempty"`
	// TODO: investigate possibility of 'ImageUrl' field removal
	ImageUrl string `json:"image,omitempty"`
}

type refreshDefinitionsModel struct {
	ImageRepositories *string `json:"imageRespositories,omitempty"`
	ExcludeBrowsers   *string `json:"excludeBrowsers,omitempty"`
}

func Ready(c *gin.Context) {
	c.String(http.StatusOK, "ready to accept requests")
}

func IsTaskDefinitionRefreshDone(c *gin.Context) {
	if DefinitionRefreshDone {
		c.Status(http.StatusOK)
	} else {
		c.Status(http.StatusServiceUnavailable)
	}
}

func RefreshDefinitions(c *gin.Context) {
	var refreshData refreshDefinitionsModel
	err := c.ShouldBindJSON(refreshData)
	if err != nil {
		log.WithError(err).Error("Failed to parse request body")
		c.Status(http.StatusBadRequest)
		return
	}

	imageRepositories := config.Conf.ImageRepositories
	if refreshData.ImageRepositories != nil {
		imageRepositories = *refreshData.ImageRepositories
	}

	excludeBrowsers := config.Conf.ExcludeBrowsers
	if refreshData.ExcludeBrowsers != nil {
		excludeBrowsers = *refreshData.ExcludeBrowsers
	}

	log.Info("parsing images")
	images, err := images.ListImages(imageRepositories, excludeBrowsers)
	if err != nil {
		log.WithError(err).Error("Failed to list images")
		c.Status(http.StatusInternalServerError)
		return
	}

	config.Conf.ExcludeBrowsers = excludeBrowsers
	config.Conf.ImageRepositories = imageRepositories

	log.Info("updating task definitions")
	err = definitions.UpdateTaskDefinitions(images)
	if err != nil {
		log.WithError(err).Error("Failed to update task definitions")
		c.Status(http.StatusInternalServerError)
		return
	}

	c.Status(http.StatusOK)
}
