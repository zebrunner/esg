package utils

import (
	"bytes"
	"encoding/json"
	"fmt"
	log "github.com/sirupsen/logrus"
	"io"
	"net/http"
	"sort"
	"strconv"
)

type imgVersions struct {
	ImageTagDetails []struct {
		ImageTag string `json:"imageTag"`
	} `json:"imageTagDetails"`
}

func ListBrowsers() ([]string, error) {
	imgRequestUrl := "https://api.us-east-1.gallery.ecr.aws/describeImageTags"
	imgNames := []string{"chrome", "edge", "firefox", "cypress-chrome", "cypress-firefox", "cypress-edge", "cypress-chromium", "redroid"}
	images := make([]string, 0)

	for _, imgName := range imgNames {

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
			log.Debug("image: ", image)
			images = append(images, image)
		}
	}

	return images, nil
}
