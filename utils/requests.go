package utils

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"

	log "github.com/sirupsen/logrus"
	"github.com/zebrunner/esg/config"
)

var ExcludeRules = parseRules()

type imgVersions struct {
	ImageTagDetails []struct {
		ImageTag string `json:"imageTag"`
	} `json:"imageTagDetails"`
}

type excludeRules struct {
	//include rules like cypress*:10*
	ImgRules []struct {
		Rep string
		Tag string
	}
	//include rules like: cypress*, cypress-chrome, cypress*:* or cypress-chromium:*
	RepRules []struct {
		Rep string
	}
}

func parseRules() *excludeRules {
	exclRules := excludeRules{}
	if config.Conf.ExcludeBrowser == "" {
		return &exclRules
	}

	rulesArr := strings.Split(config.Conf.ExcludeBrowser, ",")

	for _, rule := range rulesArr {
		partedRule := strings.Split(rule, ":")
		if len(partedRule) == 1 || partedRule[1] == "*" {
			exclRules.RepRules = append(exclRules.RepRules, struct {
				Rep string
			}{Rep: partedRule[0]})
		} else {
			exclRules.ImgRules = append(exclRules.ImgRules, struct {
				Rep string
				Tag string
			}{Rep: partedRule[0], Tag: partedRule[1]})
		}
	}

	return &exclRules
}

func (er *excludeRules) GetSupportedReps() []string {
	if len(er.RepRules) == 0 {
		return config.SupportedRepositories
	}

	supportedReps := make([]string, 0)
	for _, repRule := range er.RepRules {
		for _, repository := range config.SupportedRepositories {
			if ok := getCheckFunction(repRule.Rep)(repository); ok {
				log.Debug("Excluded " + repository + " repository")
			} else {
				supportedReps = append(supportedReps, repository)
			}
		}
	}

	return supportedReps
}

func (er *excludeRules) isAcceptableImage(imageName, imageTag string) bool {
	if len(er.ImgRules) == 0 {
		return true
	}

	for _, rule := range er.ImgRules {
		if ok := getCheckFunction(rule.Rep)(imageName); ok {
			if ok := getCheckFunction(rule.Tag)(imageTag); ok {
				return false
			}
		}
	}

	return true
}

func getCheckFunction(rule string) func(string) bool {
	var checkFn func(string) bool
	if strings.HasSuffix(rule, "*") {
		checkFn = func(s string) bool {
			rule = strings.TrimSuffix(rule, "*")
			return strings.HasPrefix(s, rule)
		}
	} else {
		checkFn = func(s string) bool {
			return s == rule
		}
	}
	return checkFn
}

func ListBrowsers() ([]string, error) {
	imgRequestUrl := "https://api.us-east-1.gallery.ecr.aws/describeImageTags"
	images := make([]string, 0)
	supportedReps := ExcludeRules.GetSupportedReps()

	for _, imgName := range supportedReps {

		rqBody := map[string]string{
			"registryAliasName": "zebrunner",
			"repositoryName":    imgName,
		}

		body, err := json.Marshal(rqBody)
		if err != nil {
			log.WithError(err).Warn("Could not create request body")
			return nil, err
		}

		req, err := http.NewRequest(http.MethodPost, imgRequestUrl, bytes.NewBuffer(body))
		if err != nil {
			// log.WithError(err).Error("Failed to get image list")
			log.WithError(err).Warn("Could not create request")
			return nil, err
		}

		res, err := http.DefaultClient.Do(req)
		if err != nil {
			log.WithError(err).Warn("Error making http request")
			return nil, err
		}

		resBody, err := io.ReadAll(res.Body)
		if err != nil {
			log.WithError(err).Warn("Could not read response body")
			return nil, err
		}

		var versions imgVersions
		err = json.Unmarshal(resBody, &versions)
		if err != nil {
			log.WithError(err).Warn("Could not read response body")
			return nil, err
		}

		sort.Slice(versions.ImageTagDetails, func(i, j int) bool {
			v1 := versions.ImageTagDetails[i].ImageTag
			v2 := versions.ImageTagDetails[j].ImageTag
			if v1 == "latest" {
				return false
			} else if v2 == "latest" {
				return true
			}

			v1f, err := strconv.ParseFloat(v1, 64)
			if err != nil {
				return false
			}
			v2f, err := strconv.ParseFloat(v2, 64)
			if err != nil {
				return false
			}

			return v1f < v2f
		})

		for _, tag := range versions.ImageTagDetails {
			image := fmt.Sprintf("%s:%s", imgName, tag.ImageTag)
			if ExcludeRules.isAcceptableImage(imgName, tag.ImageTag) {
				log.Debug("image: ", image)
				images = append(images, image)
			} else {
				log.Debug("Excluded " + image + " image")
			}
		}
	}

	return images, nil
}
