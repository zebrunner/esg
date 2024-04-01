package handlers

import (
	"fmt"
	"net/http"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/zebrunner/esg/cachemaps/utilsmap"
	"github.com/zebrunner/esg/config"
	"github.com/zebrunner/esg/utils"

	"github.com/gin-gonic/gin"
	log "github.com/sirupsen/logrus"
)

var (
	startTime time.Time
	Revision  = "undefined"
	BuildTime = "undefined"
)

func init() {
	startTime = time.Now()
	Revision = os.Getenv("REVISION")
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
				"revission": Revision,
				"time":      BuildTime,
				"version":   config.Version,
			},
			"go": gin.H{
				"version": runtime.Version(),
			},
		},
	}
	c.JSON(http.StatusOK, status)
}

func ListDrivers(c *gin.Context) {
	// TODO: Refactor code: code must be split in few different functions
	images, err := utils.ListImages()
	if err != nil {
		log.WithError(err).Warn("Failed to get browser list")
		c.Error(utils.NotFoundApiErr("failed to get browser list")).SetType(gin.ErrorTypePublic)
		return
	}

	var browsersResponse []map[string]interface{}

	imagesPlatforms := map[string]string{
		"redroid": "android",
	}
	cypressPlatforms := map[string]string{
		"cypress-chrome":   "cypress",
		"cypress-chromium": "cypress",
		"cypress-edge":     "cypress",
		"cypress-firefox":  "cypress",
	}

	windowsPlatforms := map[string]string{
		"windows-chrome": "windows",
		"windows-edge":   "windows",
	}

	for _, image := range images {
		name := strings.Split(image, ":")[0]
		version := strings.Split(image, ":")[1]

		if version == "debug" {
			continue
		}

		if name == "edge" {
			name = "MicrosoftEdge"
		}

		browserData := map[string]interface{}{
			"name":     name,
			"version":  version,
			"platform": "linux",
		}

		if _, ok := imagesPlatforms[name]; ok {
			// hardcoded browser name and verion for ReDroid emulator
			if browserData["version"] == "latest" {
				continue
			}
			browserData["platform"] = imagesPlatforms[name]
			browserData["browserName"] = "chrome"
			browserData["browserVersion"] = "107.0"
		}

		if _, ok := cypressPlatforms[name]; ok {
			browserData["image"] = "public.ecr.aws/zebrunner/" + image
			browserData["platform"] = cypressPlatforms[name]
		}

		if platform, ok := windowsPlatforms[name]; ok {
			browserData["name"] = strings.TrimPrefix(name, "windows-")
			browserData["platform"] = platform
		}

		browsersResponse = append(browsersResponse, browserData)
	}
	c.JSON(http.StatusOK, browsersResponse)
}

func Welcome(c *gin.Context) {
	scalerVersion, err := utilsmap.GetScalerVersion()
	if err != nil {
		scalerVersion = "undefined"
	}
	welcomeMsg := fmt.Sprintf("Welcome to Zebrunner Elastic Selenium Grid!\nrouter: %s\nscaler: %s", config.Version, scalerVersion)

	c.String(http.StatusOK, welcomeMsg)
}
