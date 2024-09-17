package definitions

import (
	"fmt"
	"net/http"

	"github.com/zebrunner/esg/config"
)

const (
	IsReadyPath            ApiPath = "/refresh-complete"
	RefreshDefinitionsPath ApiPath = "/refresh-definitions"
)

type ApiPath string

func (p ApiPath) String() string {
	return string(p)
}

func (p ApiPath) StringUrl() string {
	return fmt.Sprintf("http://%s%s", config.Conf.DefinitionsConnectionString, p.String())
}

func IsTaskDefinitionRefreshDone() (bool, error) {
	req, err := http.NewRequest(http.MethodGet, IsReadyPath.StringUrl(), nil)
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
