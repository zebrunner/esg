package definitions

import (
	"fmt"
	"net/http"
)

const (
	IsReadyPath            ApiPath = "/ready"
	GetImagesPath          ApiPath = "/images"
	RefreshDefinitionsPath ApiPath = "/refresh-definitions"

	E3SDefinitionsPort = ":5555"
)

type ApiPath string

func (p ApiPath) String() string {
	return string(p)
}

func IsTaskDefinitionRefreshDone() (bool, error) {
	req, err := http.NewRequest(http.MethodGet, fmt.Sprintf("http://localhost:%s", E3SDefinitionsPort), nil)
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
