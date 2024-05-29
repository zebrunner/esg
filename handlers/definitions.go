package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

var (
	DefinitionRefreshDone = false
)

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

func GetImages(c *gin.Context) {

}

func RefreshDefinitions(c *gin.Context) {

}
