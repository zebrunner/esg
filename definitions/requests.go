package definitions

import (
	"fmt"
	"net/http"

	"github.com/zebrunner/esg/config"
)

const (
	IsReadyPath            ApiPath = "/refresh-complete"
	GetImagesPath          ApiPath = "/images"
	RefreshDefinitionsPath ApiPath = "/refresh-definitions"
)

type ApiPath string

func (p ApiPath) String() string {
	return string(p)
}

func IsTaskDefinitionRefreshDone() (bool, error) {
	req, err := http.NewRequest(http.MethodGet, fmt.Sprintf("http://%s", config.Conf.DefinitionsConnectionString), nil)
	if err != nil {
		return false, err
	}

	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return false, err
	}

	if res.StatusCode == http.StatusOK {
		return true, nil
	} else if res.StatusCode == http.StatusServiceUnavailable {
		return false, nil
	} else {
		return false, fmt.Errorf("wrong status code: %v", res.StatusCode)
	}
}
