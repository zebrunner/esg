// Package playwright talks to the browser supervisor that owns the Playwright server inside a task.
package playwright

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/zebrunner/esg/environment/network"
)

const (
	readyStatus = "ready"

	pollInterval   = 500 * time.Millisecond
	requestTimeout = 2 * time.Minute
)

var httpClient = &http.Client{Timeout: requestTimeout}

// State mirrors the supervisor snapshot returned by its health and refresh endpoints.
type State struct {
	Status      string   `json:"status"`
	BrowserType string   `json:"browserType"`
	Generation  int      `json:"generation"`
	Headless    bool     `json:"headless"`
	Args        []string `json:"args"`
	WsEndpoint  string   `json:"wsEndpoint"`
}

// RefreshOptions leaves every omitted field at the value the supervisor is already using.
type RefreshOptions struct {
	BrowserType string  `json:"browserType,omitempty"`
	Args        *string `json:"args,omitempty"`
	Headless    *bool   `json:"headless,omitempty"`
}

// ControlError carries the supervisor status code so callers can tell a conflict from a failure.
type ControlError struct {
	StatusCode int
	Message    string
}

func (e *ControlError) Error() string {
	return fmt.Sprintf("browser control returned %d: %s", e.StatusCode, e.Message)
}

// Refresh swaps the running browser and returns the supervisor state once the new one is up.
func Refresh(net *network.NetworkConfiguration, opts RefreshOptions) (*State, error) {
	url, ok := net.GetUrl("playwrightRefresh")
	if !ok {
		return nil, fmt.Errorf("failed to get url of playwright control")
	}

	payload, err := json.Marshal(opts)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequest(http.MethodPost, url.String(), bytes.NewReader(payload))
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

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != http.StatusOK {
		return nil, &ControlError{StatusCode: resp.StatusCode, Message: string(bytes.TrimSpace(body))}
	}

	var state State
	err = json.Unmarshal(body, &state)
	if err != nil {
		return nil, err
	}

	return &state, nil
}

// WaitReady polls the supervisor until it reports a running browser or ctx expires.
func WaitReady(ctx context.Context, net *network.NetworkConfiguration) (*State, error) {
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	var lastErr error
	for {
		state, err := health(net)
		if err == nil && state.Status == readyStatus {
			return state, nil
		}

		if err != nil {
			lastErr = err
		} else {
			lastErr = fmt.Errorf("browser is not ready. status=%s", state.Status)
		}

		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("timed out waiting for browser: %w", lastErr)
		case <-ticker.C:
		}
	}
}

func health(net *network.NetworkConfiguration) (*State, error) {
	url, ok := net.GetUrl("playwrightHealth")
	if !ok {
		return nil, fmt.Errorf("failed to get url of playwright health")
	}

	req, err := http.NewRequest(http.MethodGet, url.String(), nil)
	if err != nil {
		return nil, err
	}

	req.Host = "localhost"

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("status code is %v", resp.StatusCode)
	}

	var state State
	err = json.NewDecoder(resp.Body).Decode(&state)
	if err != nil {
		return nil, err
	}

	return &state, nil
}
