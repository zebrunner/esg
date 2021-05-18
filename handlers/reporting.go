package handlers

import (
	"github.com/gin-gonic/gin"
	"github.com/zebrunner/esg/service"
	"net/http"
	"time"
)

var (
	startTime time.Time
)

func init() {
	startTime = time.Now()
}

func Ping(c *gin.Context) {
	c.JSON(http.StatusOK, map[string]interface{}{
		"uptime": time.Since(startTime),
		"version": "1.0.0", // TODO: Get current version for project
	})
}

func ClusterStatus(c *gin.Context) {
	status, err := service.GetTasksCount()
	if err != nil {
		c.Error(err)
		return
	}
	c.JSON(http.StatusOK, status)
}

func Welcome(c *gin.Context) {
	c.String(http.StatusOK, "Welcome to Zebrunner Elastic Selenoid Grid!")
}
