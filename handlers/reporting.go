
package handlers

import (
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
	images, err := findImages()
	if err != nil {
		_ = c.Error(err).SetType(gin.ErrorTypePublic)
		return
	}
	browsersResponse := formBrowsersResponse(images)
	c.JSON(http.StatusOK, browsersResponse)
}

func findImages() (images []string, err error) {
	if config.Conf.BrowsersFile != "" {
		var text []byte
		text, err = os.ReadFile(config.Conf.BrowsersFile)
		if err != nil {
			log.WithError(err).Error("Failed to read file browsers.txt")
			return nil, err
		}
		lines := strings.Split(string(text), "\n")

		for _, line := range lines {
			if line != "" {
				images = append(images, line)
			}
		}
	} else {
		images, err = service.ListBrowsers()
		if err != nil {
			log.WithError(err).Warn("Failed to get browser list")
			return nil, err
		}
	}
	return images, err
}

func formBrowsersResponse(images []string) (browsersResponse []interface{})  {
	redroidPlatforms := map[string]string{
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

		if platform, ok := redroidPlatforms[name]; ok {
			// hardcoded browser name and version for ReDroid emulator
			browserData["platform"] = platform
			browserData["browserName"] = "chrome"
			browserData["browserVersion"] = "107.0"
		}

		if platform, ok := cypressPlatforms[name]; ok {
			browserData["image"] = "public.ecr.aws/zebrunner/" + image
			browserData["platform"] = platform
		}

		browsersResponse = append(browsersResponse, browserData)
	}
	return browsersResponse
}

func Welcome(c *gin.Context) {
	c.String(http.StatusOK, "Welcome to Zebrunner Elastic Selenium Grid!")
}
