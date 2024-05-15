package selenium

import (
	"fmt"
	"net/http"
	"net/url"

	log "github.com/sirupsen/logrus"
)

func SendToRecorder(u *url.URL) error {
	req, err := http.NewRequest(http.MethodPost, u.String(), nil)
	if err != nil {
		return err
	}

	req.Host = "localhost"
	
	log.WithField("Url", req.URL.String()).Info("recorder req url")
	resp, err := httpClient.Do(req)
	if err != nil {
		return err
	}

	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("status code is %v", resp.StatusCode)
	}

	return nil
}
