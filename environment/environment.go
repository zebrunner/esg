package environment

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/ecs"
	"github.com/zebrunner/esg/capabilities"
	"github.com/zebrunner/esg/config"
)

const (
	linuxPlatform   = "linux"
	androidPlatform = "android"
	redroidDevice   = "redroid"
	anyPlatform     = "any"
	imageRepo       = "public.ecr.aws/zebrunner/" //public zebrunner ECR docker registry
)

type NetworkConfiguration struct {
	IP        string
	Endpoints map[string]*Endpoint
}

type Endpoint struct {
	HostPort      int64
	ContainerPort int64
	Path          string
}

type ExecutionEnvironment struct {
	TaskDefinitionFamily string
	Endpoints            map[string]*Endpoint
	Containers           []*Container
	Capabilities         *capabilities.Capabilities
	Volumes              map[string]volume
	Network              *NetworkConfiguration
	TaskId               string
}

func (e *ExecutionEnvironment) ContainerDefinitions() []*ecs.ContainerDefinition {
	definitions := []*ecs.ContainerDefinition{}

	for _, c := range e.Containers {
		cpu := c.Cpu()
		memory := c.Memory()
		memoryReservation := c.MemoryReservation()
		definition := ecs.ContainerDefinition{
			Name:              &c.Name,
			Image:             &c.Image,
			Cpu:               &cpu,
			Memory:            &memory,
			MemoryReservation: &memoryReservation,
			Essential:         &c.Essential,
			Privileged:        &c.Privileged,
		}

		links := []*string{}
		for _, link := range c.Links {
			linkName := link //local declaration required to append all values
			links = append(links, &linkName)
		}
		definition.Links = links

		volumes := []*ecs.MountPoint{}
		for _, volumeName := range c.Mounts {
			// local declarations required to append all values
			volume := e.Volumes[volumeName]
			name := volumeName
			containerPath := volume.ContainerPath
			readOnly := volume.ReadOnly
			volumes = append(volumes, &ecs.MountPoint{
				ContainerPath: &containerPath,
				SourceVolume:  &name,
				ReadOnly:      &readOnly,
			})
		}
		definition.MountPoints = volumes

		portMappings := []*ecs.PortMapping{}
		for _, mapping := range c.Ports {
			m := ecs.PortMapping{
				ContainerPort: aws.Int64(mapping.ContainerPort),
				HostPort:      aws.Int64(mapping.HostPort),
			}
			portMappings = append(portMappings, &m)
		}
		definition.PortMappings = portMappings

		definitions = append(definitions, &definition)
	}

	return definitions
}

func (e *ExecutionEnvironment) ContainerOverrides() []*ecs.ContainerOverride {
	overrides := []*ecs.ContainerOverride{}
	for _, container := range e.Containers {
		cpu := container.Cpu()
		memory := container.Memory()
		memoryReservation := container.MemoryReservation()
		override := ecs.ContainerOverride{
			Name:              &container.Name,
			Cpu:               &cpu,
			Memory:            &memory,
			MemoryReservation: &memoryReservation,
		}

		env := []*ecs.KeyValuePair{}
		for k, v := range container.Env {
			// need to declare local variables to provide as pointer later
			key := k
			value := v
			env = append(env, &ecs.KeyValuePair{Name: &key, Value: &value})
		}
		override.Environment = env
		overrides = append(overrides, &override)
	}

	return overrides
}

func Build(workspace string, caps *capabilities.Capabilities, conf *config.Config) (*ExecutionEnvironment, error) {
	platform := strings.ToLower(caps.PlatformName)
	if platform == androidPlatform {
		if strings.ToLower(caps.DeviceName) == redroidDevice {
			return buildAppiumRedroid(workspace, caps, conf)
		}
		return nil, fmt.Errorf("device is not supported. deviceName=%s", caps.DeviceName)
	} else if platform == linuxPlatform || platform == "" || platform == anyPlatform {
		return buildBrowser(workspace, caps, conf)
	}

	return nil, fmt.Errorf("unsupported platform name. platformName=%s", caps.PlatformName)
}

func (e *ExecutionEnvironment) GetPorts() map[string]portMapping {
	ports := map[string]portMapping{}
	for _, c := range e.Containers {
		for name, mapping := range c.Ports {
			ports[name] = mapping
		}
	}

	return ports
}

func (n *NetworkConfiguration) GetUrl(endpointName string) (u *url.URL, ok bool) {
	endpoint, ok := n.Endpoints[endpointName]
	if !ok {
		return nil, false
	}

	ip := n.IP
	if ip == "" {
		return nil, false
	}

	host := ip + ":" + strconv.FormatInt(endpoint.HostPort, 10)
	return &url.URL{Scheme: "http", Host: host, Path: endpoint.Path}, true
}

func buildImage(caps *capabilities.Capabilities) (string, error) {
	platformName := strings.ToLower(caps.PlatformName)
	deviceName := strings.ToLower(caps.DeviceName)

	if platformName == anyPlatform || platformName == "" {
		platformName = "linux"
	}

	if platformName == androidPlatform && deviceName == redroidDevice {
		name := redroidDevice
		version := caps.PlatformVersion
		if version == "" {
			version = "latest"
		}
		return imageRepo + name + ":" + version, nil
	} else if platformName == linuxPlatform {
		name := strings.ToLower(caps.BrowserName)
		name = remapName(name)
		version := strings.ToLower(caps.BrowserVersion)
		version = remapVersion(version)

		return imageRepo + name + ":" + version, nil
	} else {
		return "", fmt.Errorf("filed to build container image name. unsupported platform specified. platformName=%s", caps.PlatformName)
	}
}

func buildTaskDefinitionFamily(caps *capabilities.Capabilities) string {
	familyParts := []string{}
	platformName := strings.ToLower(caps.PlatformName)

	if caps.PlatformName == "" || platformName == "any" {
		platformName = "linux"
	}

	familyParts = append(familyParts, platformName)

	deviceName := strings.ToLower(caps.DeviceName)
	if deviceName != "" {
		familyParts = append(familyParts, deviceName)
		if deviceName == "redroid" {
			familyParts = append(familyParts, "11")
		}
	}

	browserName := strings.ToLower(caps.BrowserName)
	if browserName != "" && deviceName != "redroid" {
		familyParts = append(familyParts, remapName(browserName))
		browserVersion := strings.ToLower(caps.BrowserVersion)
		browserVersion = remapVersion(browserVersion)
		browserVersion = strings.Replace(browserVersion, ".", "-", -1)
		familyParts = append(familyParts, browserVersion)
	}

	return strings.Join(familyParts, "-")
}

func remapName(name string) string {
	remapName := map[string]string{
		"microsoftedge": "edge",
		"operablink":    "opera",
	}
	if newName, ok := remapName[name]; ok {
		return newName
	}

	return name
}

func remapVersion(version string) string {
	remapVersion := map[string]string{
		"":     "latest",
		"null": "latest",
	}
	if newVersion, ok := remapVersion[version]; ok {
		return newVersion
	}

	return version
}
