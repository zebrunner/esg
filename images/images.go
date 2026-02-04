package images

import (
	"bytes"
	"context"
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
	envtype "github.com/zebrunner/esg/environment/envType"

	"github.com/zebrunner/esg/service"
	"github.com/zebrunner/esg/utils"
)

const (
	zebrunnerPublicRegistry = "zebrunner"
)

type Image struct {
	RepositoryName string
	BrowserName    string
	Tag            string
	Platform       envtype.ENV_TYPE
	RegistryUri    string
}

func (i Image) String() string {
	return fmt.Sprintf("%s:%s", i.RepositoryName, i.Tag)
}

func (i Image) GetUrl() string {
	if i.RegistryUri != "" {
		return fmt.Sprintf("%s/%s", i.RegistryUri, i.String())
	} else {
		return i.String()
	}
}

func (i Image) GetMockCapabilities() ([]*capabilities.Capabilities, error) {
	getCapsFn, err := i.Platform.GetMockCapsBuilder()
	if err != nil {
		return nil, err
	}

	capsList, err := getCapsFn(i.BrowserName, i.Tag)
	if err != nil {
		return nil, err
	}

	return capsList, nil
}

func GetGenericImage(genericImg string) (*Image, error) {
	if genericImg == "" {
		return nil, fmt.Errorf("generic image uri is empty")
	}

	// Basic validation
	if strings.HasSuffix(genericImg, "/") {
		return nil, fmt.Errorf("invalid generic image: trailing slash")
	}
	if strings.HasPrefix(genericImg, ":") {
		return nil, fmt.Errorf("invalid generic image: missing repository name")
	}

	repo := genericImg
	tag := "latest"

	// Split tag only after last slash
	lastSlash := strings.LastIndex(genericImg, "/")
	lastColon := strings.LastIndex(genericImg, ":")

	if lastColon > lastSlash {
		repo = genericImg[:lastColon]
		tag = genericImg[lastColon+1:]
		if tag == "" {
			tag = "latest"
		}
	}

	if repo == "" {
		return nil, fmt.Errorf("invalid generic image: empty repository name")
	}

	return &Image{
		RepositoryName: repo,
		BrowserName:    GENERIC.GetBrowserName(),
		Platform:       envtype.GENERIC,
		Tag:            tag,
	}, nil
}

func ImageFromString(image string, tag string) (*Image, error) {
	repository, err := RepositoryFromString(image)
	if err != nil {
		return nil, err
	}

	return &Image{
		RepositoryName: repository.String(),
		BrowserName:    repository.GetBrowserName(),
		Platform:       repository.GetPlatform(),
		Tag:            tag,
	}, nil
}

func imageComparator(a Image, b Image) int {
	if a.RepositoryName != b.RepositoryName {
		return boolToInt(a.RepositoryName > b.RepositoryName)
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

func getRules(rules string) excludeRules {
	if rules == "" {
		log.Trace("No exclude rules were found")
		return nil
	}

	var er excludeRules
	rulesArr := strings.Split(rules, ",")
	for _, rule := range rulesArr {
		parsedRule := fmt.Sprintf("^%s$", rule)
		er = append(er, parsedRule)
	}

	return er
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
		repository, err := RepositoryFromString(repositoryName)
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

		images := make([]Image, 0, len(versions.ImageTagDetails))
		for _, tag := range versions.ImageTagDetails {
			images = append(images, Image{
				RepositoryName: repository.String(),
				BrowserName:    repository.GetBrowserName(),
				Platform:       repository.GetPlatform(),
				Tag:            tag.ImageTag,
				RegistryUri:    config.ZebrunnerEcrRegistryUri,
			})
		}

		imgsCh <- images
	}
}

func buildImagesFromPrivate(wg *sync.WaitGroup, registryId string, repositories []string, imgsCh chan<- []Image, errorCh chan<- error) {
	defer wg.Done()
	ctx := context.Background()
	imgsDetails, err := service.DescribeImages(ctx, registryId, repositories)
	if err != nil {
		log.WithError(err).Error("Failed to describe private ecr images")
		errorCh <- err
		return
	}

	images := make([]Image, 0, len(imgsDetails))
	for _, details := range imgsDetails {
		if details.RepositoryName != nil && len(details.ImageTags) > 0 {
			repository, err := RepositoryFromString(*details.RepositoryName)
			if err != nil {
				log.WithError(err).Error("Failed to get repository from string")
				errorCh <- err
				return
			}

			for _, imgTag := range details.ImageTags {
				if imgTag != "" {
					images = append(images, Image{
						RepositoryName: repository.String(),
						BrowserName:    repository.GetBrowserName(),
						Platform:       repository.GetPlatform(),
						Tag:            imgTag,
						RegistryUri:    fmt.Sprintf("%s.dkr.ecr.%s.amazonaws.com", registryId, config.Conf.AwsRegion),
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
		err = fmt.Errorf("registry ally is empty")
		return
	}
	regAlly = regArr[0]

	if regArr[1] == "" {
		err = fmt.Errorf("repositories are not passed for %s registry ally", regAlly)
		return
	}

	repositories = strings.Split(regArr[1], ",")
	for i := 0; i < len(repositories); i++ {
		repositories[i] = strings.TrimSpace(repositories[i])
	}

	return
}

func ListImages(imageRepositories string, rules string) ([]Image, error) {
	imgsCh := make(chan []Image)
	errCh := make(chan error)
	wg := sync.WaitGroup{}

	registrieswWithRespositories := strings.Split(imageRepositories, ";")
	for _, registryWithRepositories := range registrieswWithRespositories {
		registryId, repositories, err := splitRegistryAndRepositories(registryWithRepositories)
		if err != nil {
			return nil, err
		}

		wg.Add(1)
		if strings.ToLower(registryId) == zebrunnerPublicRegistry {
			go buildImagesFromPublic(&wg, repositories, imgsCh, errCh)
		} else {
			go buildImagesFromPrivate(&wg, registryId, repositories, imgsCh, errCh)
		}
	}

	doneCh := make(chan interface{})
	go utils.WaitForAllThreads(&wg, doneCh)

	excludeRules := getRules(rules)
	images := make([]Image, 0)
out:
	for {
		select {
		case imgsToAppend := <-imgsCh:
			for _, img := range imgsToAppend {
				imgStr := img.String()
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

	return images, nil
}
