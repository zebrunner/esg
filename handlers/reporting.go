package handlers

import (
	"fmt"
	"net/http"
	"os"
	"runtime"
	"time"

	"github.com/zebrunner/esg/cachemaps/utilsmap"
	"github.com/zebrunner/esg/config"

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
	// TODO: Refactor code: code must be split in few different functions
	// TODO: redirect to e3s-definitions

	// images, err := environment.ListImages()
	// if err != nil {
	// 	log.WithError(err).Warn("Failed to get browser list")
	// 	c.Error(utils.NotFoundApiErr("failed to get browser list")).SetType(gin.ErrorTypePublic)
	// 	return
	// }

	// var browsersResponse []map[string]interface{}

	// imagesPlatforms := map[string]string{
	// 	"redroid": "android",
	// }
	// cypressPlatforms := map[string]string{
	// 	"cypress-chrome":   "cypress",
	// 	"cypress-chromium": "cypress",
	// 	"cypress-edge":     "cypress",
	// 	"cypress-firefox":  "cypress",
	// }

	// windowsPlatforms := map[string]string{
	// 	"windows-chrome": "windows",
	// 	"windows-edge":   "windows",
	// }

	// for _, image := range images {

	// 	if image.Tag == "debug" {
	// 		continue
	// 	}
		
	// 	if image.Repository == "edge" {
	// 		image.Repository = "MicrosoftEdge"
	// 	}

	// 	browserData := map[string]interface{}{
	// 		"name":     image.Repository,
	// 		"version":  image.Tag,
	// 		"platform": "linux",
	// 	}

	// 	if _, ok := imagesPlatforms[image.Repository]; ok {
	// 		// hardcoded browser name and verion for ReDroid emulator
	// 		if browserData["version"] == "latest" {
	// 			continue
	// 		}
	// 		browserData["platform"] = imagesPlatforms[image.Repository]
	// 		browserData["browserName"] = "chrome"
	// 		browserData["browserVersion"] = "107.0"
	// 	}

	// 	if _, ok := cypressPlatforms[image.Repository]; ok {
	// 		browserData["image"] = image.GetImageUri()
	// 		browserData["platform"] = cypressPlatforms[image.Repository]
	// 	}

	// 	if platform, ok := windowsPlatforms[image.Repository]; ok {
	// 		browserData["name"] = strings.TrimPrefix(image.Repository, "windows-")
	// 		browserData["platform"] = platform
	// 	}

	// 	browsersResponse = append(browsersResponse, browserData)
	// }
	// c.JSON(http.StatusOK, browsersResponse)
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
