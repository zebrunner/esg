package handlers

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"runtime"
	"strings"
	"time"

	log "github.com/sirupsen/logrus"
	"github.com/zebrunner/esg/cachemaps/utilsmap"
	"github.com/zebrunner/esg/config"
	"github.com/zebrunner/esg/task-definitions-service/definitions"

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
	stream, err := definitions.GetClient().GetImages(context.Background(), nil)
	if err != nil {
		c.Status(http.StatusInternalServerError)
		log.WithError(err).Error("Failed to list images from task-definitions server")
		return
	}

	images := make([]definitions.Image, 0, 0)
CYCLE:
	for true {
		image, err := stream.Recv()
		if err != nil {
			if err == io.EOF {
				break CYCLE
			}
			log.WithError(err).Error("Failed to receive image")
			c.Status(http.StatusInternalServerError)
			return
		}
		// -debug images should not be added to the reporting, it should be available silently
		if strings.Contains(image.Version, "-debug") {
			continue
		}
		images = append(images, *image)
	}
	c.JSON(http.StatusOK, images)
}

func Welcome(c *gin.Context) {
	scalerVersion, err := utilsmap.ScalerVersion.Get()
	if err != nil {
		log.WithError(err).Trace("Failed to get scaler's version")
		scalerVersion = "undefined"
	}

	taskDefVersion, err := utilsmap.TaskDefinitionsVersion.Get()
	if err != nil {
		log.WithError(err).Trace("Failed to get task-definition's version")
		taskDefVersion = "undefined"
	}

	welcomeMsg := fmt.Sprintf("Welcome to Zebrunner Elastic Selenium Grid!\nrouter: %s\nscaler: %s\ntask-definitions: %s", config.Version, scalerVersion, taskDefVersion)

	c.String(http.StatusOK, welcomeMsg)
}

func WelcomeWithInstallationRef(c *gin.Context) {
	scalerVersion, err := utilsmap.ScalerVersion.Get()
	if err != nil {
		log.WithError(err).Trace("Failed to get scaler's version")
		scalerVersion = "undefined"
	}

	taskDefVersion, err := utilsmap.TaskDefinitionsVersion.Get()
	if err != nil {
		log.WithError(err).Trace("Failed to get task-definition's version")
		taskDefVersion = "undefined"
	}

	htmlStr := fmt.Sprintf("<html><body>Welcome to Zebrunner Elastic Selenium Grid! AWS cluster is not configured correctly."+
		"<br>router: %[1]s"+
		"<br>scaler: %s"+
		"<br>task-definitions: %s"+
		"<br><a href=https://github.com/zebrunner/e3s/blob/%[1]v/docs/installation.md>Documentation</a></body></html>",
		config.Version, scalerVersion, taskDefVersion)

	c.Writer.WriteHeader(http.StatusOK)
	c.Writer.Write([]byte(htmlStr))
	c.Abort()
}
