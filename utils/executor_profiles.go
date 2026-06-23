package utils

import (
	"encoding/json"
	"fmt"
	"strings"
)

const (
	ExecutorProfileMaven      = "maven"
	ExecutorProfilePython     = "python"
	ExecutorProfileGradle     = "gradle"
	ExecutorProfilePlaywright = "playwright"
)

type ExecutorProfiles map[string]bool

func (p ExecutorProfiles) Has(profile string) bool {
	return p[profile]
}

func (p ExecutorProfiles) merge(other ExecutorProfiles) {
	for profile := range other {
		p[profile] = true
	}
}

func ResolveExecutorProfiles(profileCaps string, profileEnv string, image string, imageProfilesConfig string) (ExecutorProfiles, error) {
	profiles := executorProfilesFromImageName(image)

	configProfiles, err := executorProfilesFromConfig(image, imageProfilesConfig)
	if err != nil {
		return nil, err
	}
	profiles.merge(configProfiles)

	capProfiles, err := executorProfilesFromCapabilities(profileCaps)
	if err != nil {
		return nil, err
	}
	profiles.merge(capProfiles)

	envProfiles, err := executorProfilesFromCapabilities(profileEnv)
	if err != nil {
		return nil, err
	}
	profiles.merge(envProfiles)

	return profiles, nil
}

func executorProfilesFromCapabilities(profileCaps string) (ExecutorProfiles, error) {
	if strings.TrimSpace(profileCaps) == "" {
		return ExecutorProfiles{}, nil
	}

	return parseExecutorProfiles(profileCaps)
}

func executorProfilesFromConfig(image string, imageProfilesConfig string) (ExecutorProfiles, error) {
	if strings.TrimSpace(imageProfilesConfig) == "" {
		return ExecutorProfiles{}, nil
	}

	var configProfiles map[string][]string
	if err := json.Unmarshal([]byte(imageProfilesConfig), &configProfiles); err != nil {
		return nil, fmt.Errorf("failed to parse generic executor image profiles config: %w", err)
	}

	profiles := ExecutorProfiles{}
	image = strings.ToLower(image)
	for profile, matchers := range configProfiles {
		profile = strings.ToLower(strings.TrimSpace(profile))
		if err := validateExecutorProfile(profile); err != nil {
			return nil, err
		}

		for _, matcher := range matchers {
			matcher = strings.ToLower(strings.TrimSpace(matcher))
			if matcher != "" && strings.Contains(image, matcher) {
				profiles[profile] = true
				break
			}
		}
	}

	return profiles, nil
}

func executorProfilesFromImageName(image string) ExecutorProfiles {
	profiles := ExecutorProfiles{}

	if strings.Contains(image, "maven") {
		profiles[ExecutorProfileMaven] = true
	}
	if strings.Contains(image, "python") || strings.Contains(image, "amancevice/pandas") {
		profiles[ExecutorProfilePython] = true
	}
	if strings.Contains(image, "gradle") {
		profiles[ExecutorProfileGradle] = true
	}
	if strings.Contains(image, "playwright") || strings.Contains(image, "node") {
		profiles[ExecutorProfilePlaywright] = true
	}

	return profiles
}

func parseExecutorProfiles(profilesRaw string) (ExecutorProfiles, error) {
	profiles := ExecutorProfiles{}
	for _, profile := range strings.Split(profilesRaw, ",") {
		profile = strings.ToLower(strings.TrimSpace(profile))
		if profile == "" {
			continue
		}

		if err := validateExecutorProfile(profile); err != nil {
			return nil, err
		}
		profiles[profile] = true
	}

	if len(profiles) == 0 {
		return nil, fmt.Errorf("generic executor profiles are empty")
	}

	return profiles, nil
}

func validateExecutorProfile(profile string) error {
	switch profile {
	case ExecutorProfileMaven, ExecutorProfilePython, ExecutorProfileGradle, ExecutorProfilePlaywright:
		return nil
	default:
		return fmt.Errorf("unsupported generic executor profile: %s", profile)
	}
}
