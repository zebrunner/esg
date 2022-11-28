
package handlers

import (
	"io/ioutil"
	"net/http"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/zebrunner/esg/config"
	"github.com/zebrunner/esg/service"

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

func ListDrivers(c *gin.Context) {
	// TODO: Refactor code: code must be split in few different functions
	var images []string

	if config.Conf.BrowsersFile != "" {
		text, err := ioutil.ReadFile(config.Conf.BrowsersFile)
		if err != nil {
			log.WithError(err).Error("Failed to read file browsers.txt")
			_ = c.Error(err)
			return
		}
		lines := strings.Split(string(text), "\n")

		for _, line := range lines {
			if line != "" {
				images = append(images, line)
			}
		}
	} else {
		imgs, err := service.ListBrowsers()
		if err != nil {
			log.WithError(err).Warn("Failed to get browser list")
			_ = c.Error(err).SetType(gin.ErrorTypePublic)
			return
		}
		images = imgs
	}

	var browsersResponse []map[string]interface{}

	imagesPlatforms := map[string]string{
		"redroid": "android",
	}
        cypressPlatforms := map[string]string{
                "cypress-chrome": "cypress",
                "cypress-chromium": "cypress",
                "cypress-edge": "cypress",
                "cypress-firefox": "cypress",
        }

	for _, image := range images {
		name := strings.Split(image, ":")[0]
		version := strings.Split(image, ":")[1]

		if version == "latest" || version == "debug" {
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
