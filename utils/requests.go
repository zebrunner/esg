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

type imgVersions struct {
	ImageTagDetails []struct {
		ImageTag string `json:"imageTag"`
	} `json:"imageTagDetails"`
}

type excludeRules []string

func getRules() excludeRules {
	if config.Conf.ExcludeBrowsers == "" {
		log.Trace("No exclude rules were found")
		return nil
	}

	var er excludeRules
	er.parseRules()
	return er
}

func (er *excludeRules) parseRules() {
	rulesArr := strings.Split(config.Conf.ExcludeBrowsers, ",")
	for _, rule := range rulesArr {
		parsedRule := fmt.Sprintf("^%s$", rule)
		*er = append(*er, parsedRule)
	}
}

func (er excludeRules) isAcceptableImage(image string) bool {
	if len(er) == 0 {
		return true
	}

	for _, rule := range er {
		if ok, _ := regexp.MatchString(rule, image); ok {
			return false
		}
	}

	return true
}

func ListBrowsers() ([]string, error) {
	imgRequestUrl := "https://api.us-east-1.gallery.ecr.aws/describeImageTags"
	images := make([]string, 0)
	var excludeRules = getRules()

	for _, imgName := range config.SupportedRepositories {

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
			if excludeRules.isAcceptableImage(image) {
				log.Debug("image: ", image)
				images = append(images, image)
			} else {
				log.Debug("Excluded " + image + " image")
			}
		}
	}

	return images, nil
}
