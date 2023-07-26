package handlers

import (
	"net/http"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/zebrunner/esg/utils"

	"github.com/gin-gonic/gin"
	log "github.com/sirupsen/logrus"
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
		"version": Version,
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

func ListDrivers(c *gin.Context) {
	// TODO: Refactor code: code must be split in few different functions
	images, err := utils.ListBrowsers()
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

		browsersResponse = append(browsersResponse, browserData)
	}
	c.JSON(http.StatusOK, browsersResponse)
}

func Welcome(c *gin.Context) {
	c.String(http.StatusOK, "Welcome to Zebrunner Elastic Selenium Grid!")
}
