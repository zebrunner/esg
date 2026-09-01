package selenium

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/zebrunner/esg/environment/network"
)

const recorderRotateTimeout = 2 * time.Minute

// RotateResult reports the artifact scopes on either side of a recorder rotation.
type RotateResult struct {
	ArtifactID         string `json:"artifactId"`
	PreviousArtifactID string `json:"previousArtifactId"`
}

// StartRecording asks the recorder container to start the session recording.
func StartRecording(network *network.NetworkConfiguration) error {
	url, ok := network.GetUrl("recorderStart")
	if !ok {
		return fmt.Errorf("failed to get url of recorder")
	}

	req, err := http.NewRequest(http.MethodPost, url.String(), nil)
	if err != nil {
		return err
	}

	req.Host = "localhost"

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

// RotateRecording publishes the artifacts collected so far and reopens collection under newUUID.
// The shared client has no timeout, so the deadline here keeps a stalled recorder off the refresh path.
func RotateRecording(network *network.NetworkConfiguration, newUUID string) (*RotateResult, error) {
	url, ok := network.GetUrl("recorderRotate")
	if !ok {
		return nil, fmt.Errorf("failed to get url of recorder rotate")
	}

	payload, err := json.Marshal(map[string]string{"routerUuid": newUUID})
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(context.Background(), recorderRotateTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url.String(), bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}

	req.Host = "localhost"
	req.Header.Set("Content-Type", "application/json")

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}

	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("status code is %v", resp.StatusCode)
	}

	var result RotateResult
	err = json.NewDecoder(resp.Body).Decode(&result)
	if err != nil {
		return nil, err
	}

	return &result, nil
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

func finishRecording(network *network.NetworkConfiguration) error {
	url, ok := network.GetUrl("recorderFinish")
	if !ok {
		return fmt.Errorf("failed to get url of recorder finish")
	}

	req, err := http.NewRequest(http.MethodDelete, url.String(), nil)
	if err != nil {
		return err
	}

	req.Host = "localhost"

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
