package service

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"

	log "github.com/sirupsen/logrus"
	"github.com/zebrunner/esg/config"
	"github.com/zebrunner/esg/utils"
)

type imgVersions struct {
	ImageTagDetails []struct {
		ImageTag string `json:"imageTag"`
	} `json:"imageTagDetails"`
}

type image struct {
	repository string
	tag        string
}

func (i image) toString() string {
	return fmt.Sprintf("%s:%s", i.repository, i.tag)
}

func imageComparator(a image, b image) int {
	if a.repository != b.repository {
		return boolToInt(a.repository > b.repository)
	}

	if a.tag == b.tag {
		return 0
	}

	if a.tag == "latest" {
		return -1
	} else if b.tag == "latest" {
		return 1
	}

	aArr := strings.Split(a.tag, ".")
	bArr := strings.Split(b.tag, ".")

	for i := 0; i < len(aArr) && i < len(bArr); i++ {
		aInt, errA := strconv.ParseInt(aArr[i], 10, 64)
		bInt, errB := strconv.ParseInt(bArr[i], 10, 64)

		if errA != nil {
			if errB != nil {
				return boolToInt(aArr[i] > bArr[i])
			}

			return -1
		} else if errB != nil {
			return 1
		}

		if aInt != bInt {
			return boolToInt(aInt < bInt)
		}
	}

	return boolToInt(len(bArr) > len(aArr))
}

func boolToInt(b bool) int {
	index := -1
	if b {
		index = 1
	}

	return index
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

func buildImagesFromPublic(wg *sync.WaitGroup, repositories []string, imgsCh chan<- []image, errorCh chan<- error) {
	defer wg.Done()
	imgRequestUrl := "https://api.us-east-1.gallery.ecr.aws/describeImageTags"

	for _, repository := range repositories {
		rqBody := map[string]string{
			"registryAliasName": "zebrunner",
			"repositoryName":    repository,
		}

		body, err := json.Marshal(rqBody)
		if err != nil {
			log.WithError(err).Warn("Could not create request body")
			errorCh <- err
			return
		}

		req, err := http.NewRequest(http.MethodPost, imgRequestUrl, bytes.NewBuffer(body))
		if err != nil {
			log.WithError(err).Warn("Could not create request")
			errorCh <- err
			return
		}

		res, err := http.DefaultClient.Do(req)
		if err != nil {
			log.WithError(err).Warn("Error making http request")
			errorCh <- err
			return
		}

		resBody, err := io.ReadAll(res.Body)
		if err != nil {
			log.WithError(err).Warn("Could not read response body")
			errorCh <- err
			return
		}

		var versions imgVersions
		err = json.Unmarshal(resBody, &versions)
		if err != nil {
			log.WithError(err).Warn("Could not read response body")
			errorCh <- err
			return
		}

		images := []image{}
		for _, tag := range versions.ImageTagDetails {
			images = append(images, image{repository: repository, tag: tag.ImageTag})
		}

		imgsCh <- images
	}
}

func buildImagesFromPrivate(wg *sync.WaitGroup, registryAlly string, repositories []string, imgsCh chan<- []image, errorCh chan<- error) {
	defer wg.Done()
	imgsDetails, err := DescribeImages(registryAlly, repositories)
	if err != nil {
		errorCh <- err
		return
	}

	images := make([]image, 0, len(imgsDetails))
	for _, details := range imgsDetails {
		if details != nil && details.RepositoryName != nil && details.ImageTags != nil {
			for _, imgTag := range details.ImageTags {
				if imgTag != nil {
					images = append(images, image{repository: *details.RepositoryName, tag: *imgTag})
				}
			}
		}
	}

	imgsCh <- images
}

func ListImages() ([]string, error) {
	var excludeRules = getRules()

	supportedRepositories := []string{
		"redroid",
		"cypress-chrome",
		"cypress-chromium",
		"cypress-edge",
		"cypress-firefox",
		"windows-chrome",
		"windows-edge",
	}

	imgsCh := make(chan []image)
	errCh := make(chan error)
	wg := sync.WaitGroup{}
	if config.Conf.PrivateBrowserImages == "" {
		supportedRepositories = append(supportedRepositories, "chrome", "firefox", "edge")

		wg.Add(1)
		go buildImagesFromPublic(&wg, supportedRepositories, imgsCh, errCh)
	} else {
		wg.Add(1)
		go buildImagesFromPublic(&wg, supportedRepositories, imgsCh, errCh)

		registries := strings.Split(config.Conf.PrivateBrowserImages, ";")

		for _, registry := range registries {
			regAlly, repositories, err := splitRegistry(registry)
			if err != nil {
				return nil, err
			}

			wg.Add(1)
			go buildImagesFromPrivate(&wg, regAlly, repositories, imgsCh, errCh)
		}
	}

	doneCh := make(chan interface{})
	go utils.WaitForAllThreads(&wg, doneCh)

	images := make([]image, 0)
out:
	for {
		select {
		case imgsToAppend := <-imgsCh:
			for _, img := range imgsToAppend {
				imgStr := img.toString()
				if excludeRules.isAcceptableImage(imgStr) {
					images = append(images, img)
				} else {
					log.Debug("Excluded " + imgStr + " image for task definition update")
				}
			}
		case err := <-errCh:
			return nil, err
		case <-doneCh:
			break out
		}
	}

	slices.SortFunc(images, imageComparator)

	imagesStr := make([]string, 0, len(images))
	for _, img := range images {
		imagesStr = append(imagesStr, img.toString())
	}

	return imagesStr, nil
}

func splitRegistry(registry string) (regAlly string, repositories []string, err error) {
	regArr := strings.Split(registry, ":")
	if len(regArr) != 2 {
		err = fmt.Errorf("failed to parse private registry info")
		return
	}

	if regArr[0] == "" {
		err = fmt.Errorf("private registry ally is empty")
		return
	}
	regAlly = regArr[0]

	if regArr[1] == "" {
		err = fmt.Errorf("repositories not passed for %s registry ally", regAlly)
		return
	}
	repositories = strings.Split(regArr[1], ",")

	return
}
