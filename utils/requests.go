package utils

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"

	log "github.com/sirupsen/logrus"
	"github.com/zebrunner/esg/config"
)

var ExcludeRules excludeRules = excludeRules{isParsed: false}

type imgVersions struct {
	ImageTagDetails []struct {
		ImageTag string `json:"imageTag"`
	} `json:"imageTagDetails"`
}

type excludeRules struct {
	//ImgRules include rules like: cypress-chrome:90.0, cypress-chrome:90*, cypress*:90*, *:latest, *:9*
	ImgRules []string
	//RepRules include rules like: cypress-chrome, cypress*, *
	RepRules []string
	isParsed bool
}

func getRules() excludeRules {
	if !ExcludeRules.isParsed {
		ExcludeRules.parseRules()
	}
	return ExcludeRules
}

func (er *excludeRules) parseRules() {
	er.isParsed = true
	if config.Conf.ExcludeBrowsers == "" {
		log.Debug("No exclude rules were found " + config.Conf.ExcludeBrowsers)
		return
	}

	rulesArr := strings.Split(config.Conf.ExcludeBrowsers, ",")
	regexForRep, _ := regexp.Compile(`^[a-z\-]*[^:]$`)
	for _, rule := range rulesArr {
		parsedRule := fmt.Sprintf("^%s$", strings.ReplaceAll(rule, "*", ".*"))
		if regexForRep.MatchString(rule) {
			er.RepRules = append(er.RepRules, parsedRule)
		} else {
			er.ImgRules = append(er.ImgRules, parsedRule)
		}
	}
}

func (er *excludeRules) GetSupportedReps() []string {
	if len(er.RepRules) == 0 {
		return config.SupportedRepositories
	}

	supportedReps := make([]string, 0)
	for _, repository := range config.SupportedRepositories {
		exclude := false

		for _, repRule := range er.RepRules {
			if ok, _ := regexp.MatchString(repRule, repository); ok {
				exclude = true
				log.Debug("Excluded " + repository + " repository")
				break
			}
		}

		if !exclude {
			supportedReps = append(supportedReps, repository)
		}
	}

	return supportedReps
}

func (er *excludeRules) isAcceptableImage(image string) bool {
	if len(er.ImgRules) == 0 {
		return true
	}

	for _, rule := range er.ImgRules {
		if ok, _ := regexp.MatchString(rule, image); ok {
			return false
		}
	}

	return true
}

func ListBrowsers() ([]string, error) {
	imgRequestUrl := "https://api.us-east-1.gallery.ecr.aws/describeImageTags"
	images := make([]string, 0)
	var ExcludeRules = getRules()
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
			if ExcludeRules.isAcceptableImage(image) {
				log.Debug("image: ", image)
				images = append(images, image)
			} else {
				log.Debug("Excluded " + image + " image")
			}
		}
	}

	return images, nil
}
