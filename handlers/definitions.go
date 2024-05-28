package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/zebrunner/esg/capabilities"
)

var (
	DefinitionRefreshDone = false
)

func IsTaskDefinitionRefreshDone(c *gin.Context) {
	if DefinitionRefreshDone {
		c.Status(http.StatusOK)
	} else {
		c.Status(http.StatusServiceUnavailable)
	}
}

func BuildExecutionEnvironment(c *gin.Context) {
	if !DefinitionRefreshDone {
		c.Status(http.StatusServiceUnavailable)
		return
	}

	caps := capabilities.Capabilities{}
	err := c.ShouldBindJSON(&caps)
	if err != nil {
		c.String(http.StatusInternalServerError, "failed to get caps from body")
	}

	

}

func GetImages(c *gin.Context) {

}
