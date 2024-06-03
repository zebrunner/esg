package handlers

import (
	"fmt"
	"net/http"
	"os"
	"runtime"
	"time"

	log "github.com/sirupsen/logrus"
	"github.com/zebrunner/esg/cachemaps/utilsmap"
	"github.com/zebrunner/esg/config"
	"github.com/zebrunner/esg/definitions"

	"github.com/gin-gonic/gin"
)

var (
	startTime time.Time
	BuildTime = "undefined"
)

func init() {
	startTime = time.Now()
	BuildTime = os.Getenv("BUILD_TIME")
}

func Ping(c *gin.Context) {
	c.JSON(http.StatusOK, map[string]interface{}{
		"uptime":  time.Since(startTime),
		"version": config.Version,
	})
}

func ClusterStatus(c *gin.Context) {
	status := gin.H{
		"status": 0,
		"value": gin.H{
			"ready":   true,
			"message": "Server is running",
			"build": gin.H{
				"time":    BuildTime,
				"version": config.Version,
			},
			"go": gin.H{
				"version": runtime.Version(),
			},
		},
	}
	c.JSON(http.StatusOK, status)
}

func ListDrivers(c *gin.Context) {
	resBody, err := definitions.ListImages()
	if err != nil {
		c.Status(http.StatusInternalServerError)
		log.WithError(err).Error("Failed to list images from e3s-definitions server")
		return
	}
	
	_, err = c.Writer.Write(resBody)
	if err != nil {
		c.Status(http.StatusInternalServerError)
		log.WithError(err).Error("Failed to write body server")
		return
	}
}

func Welcome(c *gin.Context) {
	scalerVersion, err := utilsmap.GetScalerVersion()
	if err != nil {
		scalerVersion = "undefined"
	}
	welcomeMsg := fmt.Sprintf("Welcome to Zebrunner Elastic Selenium Grid!\nrouter: %s\nscaler: %s", config.Version, scalerVersion)

	c.String(http.StatusOK, welcomeMsg)
}

func WelcomeWithInstallationRef(c *gin.Context) {
	scalerVersion, err := utilsmap.GetScalerVersion()
	if err != nil {
		scalerVersion = "undefined"
	}

	htmlStr := fmt.Sprintf("<html><body>Welcome to Zebrunner Elastic Selenium Grid! AWS cluster is not configured correctly."+
		"<br>router: %[1]s"+
		"<br>scaler: %s"+
		"<br><a href=https://github.com/zebrunner/e3s/blob/%[1]v/docs/installation.md>Documentation</a></body></html>", config.Version, scalerVersion)

	c.Writer.WriteHeader(http.StatusOK)
	c.Writer.Write([]byte(htmlStr))
	c.Abort()
}
