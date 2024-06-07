package selenium

import (
	"fmt"
	"net/http"

	log "github.com/sirupsen/logrus"
	"github.com/zebrunner/esg/environment/network"
)

func startRecording(network *network.NetworkConfiguration) error {
	url, ok := network.GetUrl("recorderStart")
	if !ok {
		return fmt.Errorf("failed to get url of recorder")
	}

	req, err := http.NewRequest(http.MethodPost, url.String(), nil)
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

func stopRecording(network *network.NetworkConfiguration) error {
	url, ok := network.GetUrl("recorderStop")
	if !ok {
		return fmt.Errorf("failed to get url of recorder")
	}

	req, err := http.NewRequest(http.MethodDelete, url.String(), nil)
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
