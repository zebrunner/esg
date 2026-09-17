package utils

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
)

func ResolvePlaywrightWSEndpoint(hubURL string, e3sURL string) string {
	if parsed, err := url.Parse(hubURL); err == nil && parsed.Host != "" {
		if strings.EqualFold(parsed.Scheme, "https") {
			parsed.Scheme = "wss"
		} else {
			parsed.Scheme = "ws"
		}
		parsed.Path = "/ws/playwright"
		parsed.RawPath = ""
		parsed.RawQuery = ""
		parsed.ForceQuery = false
		parsed.Fragment = ""
		parsed.RawFragment = ""
		return parsed.String()
	}

	e3sURL = strings.ToLower(e3sURL)
	wsScheme := "ws"
	if strings.HasPrefix(e3sURL, "https") {
		wsScheme = "wss"
	}
	wsHost := strings.TrimPrefix(strings.TrimPrefix(e3sURL, "https://"), "http://")
	return fmt.Sprintf("%s://%s/ws/playwright", wsScheme, wsHost)
}

func ExtractCapabilityAsString(envVars map[string]string, capability string) (string, error) {
	zebrunnerCapsJSON, ok := envVars["ZEBRUNNER_CAPABILITIES"]
	if !ok {
		return "", fmt.Errorf("ZEBRUNNER_CAPABILITIES missing or invalid")
	}

	var capsMap map[string]string
	if err := json.Unmarshal([]byte(zebrunnerCapsJSON), &capsMap); err != nil {
		return "", fmt.Errorf("failed to parse ZEBRUNNER_CAPABILITIES: %w", err)
	}

	value := capsMap[capability]
	if value == "" {
		return "", nil
	}

	return value, nil
}
