package images

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
	"github.com/zebrunner/esg/capabilities"
	"github.com/zebrunner/esg/config"
	"github.com/zebrunner/esg/service"
	"github.com/zebrunner/esg/utils"
)

var (
	imagesArr = []Image{}
)

func GetStoredImages() []Image {
	return imagesArr
}

func StoreImages(imgs []Image) {
	imagesArr = imgs
}

type Image struct {
	Repository  supportedRepository
	Tag         string
	Platform    string
	RegistryUri string
}

func (i Image) ToString() string {
	return fmt.Sprintf("%s:%s", i.Repository.String(), i.Tag)
}

func (i Image) GetUrl() string {
	return fmt.Sprintf("%s/%s", i.RegistryUri, i.ToString())
}

func (i Image) GetMockCapabilities() ([]*capabilities.Capabilities, error) {
	getCapsFn := i.Repository.getCapsForPlaformFn()

	capsList, err := getCapsFn(i.Repository.String(), i.Tag)
	if err != nil {
		return nil, err
	}

	return capsList, nil
}

func ToImage(caps *capabilities.Capabilities) Image {

	return Image{}
}

func imageComparator(a Image, b Image) int {
	if a.Repository != b.Repository {
		return boolToInt(a.Repository > b.Repository)
	}

	if a.Tag == b.Tag {
		return 0
	}

	if a.Tag == "latest" {
		return -1
	} else if b.Tag == "latest" {
		return 1
	}

	aArr := strings.Split(a.Tag, ".")
	bArr := strings.Split(b.Tag, ".")

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

type imgVersions struct {
	ImageTagDetails []struct {
		ImageTag string `json:"imageTag"`
	} `json:"imageTagDetails"`
}

func buildImagesFromPublic(wg *sync.WaitGroup, repositories []string, imgsCh chan<- []Image, errorCh chan<- error) {
	defer wg.Done()
	imgRequestUrl := "https://api.us-east-1.gallery.ecr.aws/describeImageTags"

	for _, repositoryName := range repositories {
		repository, err := repositoryFromString(repositoryName)
		if err != nil {
			log.WithError(err).Warn("Failed to get repository from string")
			errorCh <- err
			return
		}

		rqBody := map[string]string{
			"registryAliasName": "zebrunner",
			"repositoryName":    repository.String(),
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

		images := []Image{}
		for _, tag := range versions.ImageTagDetails {
			images = append(images, Image{
				Repository:  repository,
				Tag:         tag.ImageTag,
				RegistryUri: config.ZebrunnerEcrRegistryUri,
			})
		}

		imgsCh <- images
	}
}

func buildImagesFromPrivate(wg *sync.WaitGroup, registryId string, repositories []string, imgsCh chan<- []Image, errorCh chan<- error) {
	defer wg.Done()
	imgsDetails, err := service.DescribeImages(registryId, repositories)
	if err != nil {
		log.WithError(err).Error("Failed to describe private ecr images")
		errorCh <- err
		return
	}

	images := make([]Image, 0, len(imgsDetails))
	for _, details := range imgsDetails {
		if details != nil && details.RepositoryName != nil && details.ImageTags != nil {
			repository, err := repositoryFromString(*details.RepositoryName)
			if err != nil {
				log.WithError(err).Error("Failed to get repository from string")
				errorCh <- err
				return
			}

			for _, imgTag := range details.ImageTags {
				if imgTag != nil {
					images = append(images, Image{
						Repository:  repository,
						Tag:         *imgTag,
						RegistryUri: fmt.Sprintf("arn:aws:ecr:%s:%s:repository", config.Conf.AwsRegion, registryId),
					})
				}
			}
		}
	}

	imgsCh <- images
}

func splitRegistryAndRepositories(registry string) (regAlly string, repositories []string, err error) {
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

func GenerateImages() error {
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

	imgsCh := make(chan []Image)
	errCh := make(chan error)
	wg := sync.WaitGroup{}
	if config.Conf.PrivateBrowserImages == "" {
		supportedRepositories = append(supportedRepositories, "chrome", "firefox", "edge")

		wg.Add(1)
		go buildImagesFromPublic(&wg, supportedRepositories, imgsCh, errCh)
	} else {
		wg.Add(1)
		go buildImagesFromPublic(&wg, supportedRepositories, imgsCh, errCh)

		registriesRespositories := strings.Split(config.Conf.PrivateBrowserImages, ";")

		for _, registryWithRepositories := range registriesRespositories {
			registryId, repositories, err := splitRegistryAndRepositories(registryWithRepositories)
			if err != nil {
				return err
			}

			wg.Add(1)
			go buildImagesFromPrivate(&wg, registryId, repositories, imgsCh, errCh)
		}
	}

	doneCh := make(chan interface{})
	go utils.WaitForAllThreads(&wg, doneCh)

	images := make([]Image, 0)
out:
	for {
		select {
		case imgsToAppend := <-imgsCh:
			for _, img := range imgsToAppend {
				imgStr := img.ToString()
				if excludeRules.isAcceptableImage(imgStr) {
					images = append(images, img)
				} else {
					log.Debug("Excluded " + imgStr + " image for task definition update")
				}
			}
		case err := <-errCh:
			return err
		case <-doneCh:
			break out
		}
	}

	slices.SortFunc(images, imageComparator)
	imagesArr = images

	return nil
}
