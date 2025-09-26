package utils

import (
	"encoding/json"
	"fmt"
)

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
		return "", fmt.Errorf("%s not defined in ZEBRUNNER_CAPABILITIES", capability)
	}

	return value, nil
}
