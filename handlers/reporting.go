package handlers

import (
	"net/http"
	"os"
	"runtime"
	"time"

	"github.com/gin-gonic/gin"
)

var (
	startTime time.Time
	Revision  = "undefined"
	BuildTime = "undefined"
	Version   = "undefined"
)

func init() {
	startTime = time.Now()
	Revision = os.Getenv("REVISION")
	BuildTime = os.Getenv("BUILD_TIME")
	Version = os.Getenv("VERSION")
}

func Ping(c *gin.Context) {
	c.JSON(http.StatusOK, map[string]interface{}{
		"uptime":  time.Since(startTime),
		"version": "1.0.0", // TODO: Get current version for project
	})
}

func ClusterStatus(c *gin.Context) {
	status := gin.H{
		"status": 0,
		"value": gin.H{
			"ready":   true,
			"message": "Server is running",
			"build": gin.H{
				"revission": Revision,
				"time":      BuildTime,
				"version":   Version,
			},
			"go": gin.H{
				"version": runtime.Version(),
			},
		},
	}
	c.JSON(http.StatusOK, status)
}

func Welcome(c *gin.Context) {
	c.String(http.StatusOK, "Welcome to Zebrunner Elastic Selenoid Grid!")
}
