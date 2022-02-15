package handlers

import (
	"net/http"
	"os"
	"runtime"
	"strings"
	"time"

	log "github.com/sirupsen/logrus"
	"github.com/zebrunner/esg/service"

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

func ListBrowsers(c *gin.Context) {
	browsers, err := service.ListBrowsers()
	if err != nil {
		log.WithError(err).Warn("Failed to get browser list")
		_ = c.Error(err).SetType(gin.ErrorTypePublic)
		return
	}

	browsersResponse := []map[string]interface{}{}
	for _, browser := range browsers {
		browserName := strings.Split(browser, ":")[0]
		if browserName == "edge" {
			browserName = "MicrosoftEdge"
		}

		browserData := map[string]interface{}{
			"name":     browserName,
			"version":  strings.Split(browser, ":")[1],
			"platform": "linux",
		}
		browsersResponse = append(browsersResponse, browserData)
	}
	c.JSON(http.StatusOK, browsersResponse)
}

func Welcome(c *gin.Context) {
	c.String(http.StatusOK, "Welcome to Zebrunner Elastic Selenium Grid!")
}
