package environment

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/ecs"
	"github.com/zebrunner/esg/capabilities"
)

const (
	linuxPlatform   = "linux"
	androidPlatform = "android"
	redroidDevice   = "redroid"
	anyPlatform     = "any"
	genericPlatform = "generic"
	cypressPlatform = "cypress"

	imageRepo       = "public.ecr.aws/zebrunner/" //public zebrunner ECR docker registry
	uploaderImage   = imageRepo + "uploader:2.2"
	mitmImage       = imageRepo + "mitmproxy:1.0"
	recorderImage   = imageRepo + "recorder:1.1"
	appiumImage     = imageRepo + "appium:1.4.10"
	cloneImage      = imageRepo + "git:latest"
	entrypointImage = imageRepo + "entrypoint:2.1"
	mavenImage      = imageRepo + "m2-repo-carina:1.4"
)

const (
	seleniumPort   int64 = 4444
	vncPort        int64 = 5900
	devtoolsPort   int64 = 7070
	fileserverPort int64 = 8080
	clipboardPort  int64 = 9090

	recorderCpu    int64 = 320
	recorderMemory int64 = 1024

	genericPort int64 = 22
	minCpu            = 128
	minMemory         = 256
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
		definition := ecs.ContainerDefinition{
			Name:              &c.Name,
			Image:             &c.Image,
			Cpu:               &cpu,
			Memory:            &memory,
			MemoryReservation: &memory,
			Essential:         &c.Essential,
			Privileged:        &c.Privileged,
			HealthCheck:       c.HealthCheck,
			DependsOn:         c.DependsOn,
		}

		if c.WorkingDirectory != "" {
			definition.WorkingDirectory = &c.WorkingDirectory
		}

		links := []*string{}
		for _, link := range c.Links {
			linkName := link //local declaration required to append all values
			links = append(links, &linkName)
		}
		definition.Links = links

		entrypoints := []*string{}
		for _, entrypoint := range c.EntryPoint {
			entrypointName := entrypoint //local declaration required to append all values
			entrypoints = append(entrypoints, &entrypointName)
		}
		definition.EntryPoint = entrypoints

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

		command := []*string{}
		for _, cmd := range c.Command {
			cmdName := cmd //local declaration required to append all values
			command = append(command, &cmdName)
		}
		definition.Command = command
		definitions = append(definitions, &definition)
	}

	return definitions
}

func (e *ExecutionEnvironment) ContainerOverrides() []*ecs.ContainerOverride {
	overrides := []*ecs.ContainerOverride{}
	for _, container := range e.Containers {
		cpu := container.Cpu()
		memory := container.Memory()
		override := ecs.ContainerOverride{
			Name:              &container.Name,
			Cpu:               &cpu,
			Memory:            &memory,
			MemoryReservation: &memory,
		}

		env := []*ecs.KeyValuePair{}
		for k, v := range container.Env {
			// need to declare local variables to provide as pointer later
			key := k
			value := v
			env = append(env, &ecs.KeyValuePair{Name: &key, Value: &value})
		}
		override.Environment = env

		command := []*string{}
		for _, cmd := range container.Command {
			cmdName := cmd //local declaration required to append all values
			command = append(command, &cmdName)
		}
		override.Command = command

		overrides = append(overrides, &override)

	}

	return overrides
}

func Build(workspace string, caps *capabilities.Capabilities) (*ExecutionEnvironment, error) {
	platform := strings.ToLower(caps.PlatformName)
	if platform == androidPlatform {
		if strings.ToLower(caps.DeviceName) == redroidDevice {
			return buildAppiumRedroid(workspace, caps)
		}
		return nil, fmt.Errorf("device is not supported. deviceName=%s", caps.DeviceName)
	} else if platform == genericPlatform {
		return buildGeneric(workspace, caps)
	} else if platform == cypressPlatform {
		return buildCypress(workspace, caps)
	} else if platform == linuxPlatform || platform == "" || platform == anyPlatform {
		return buildBrowser(workspace, caps)
	}

	return nil, fmt.Errorf("platform is not supported. platformName=%s", caps.PlatformName)
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
	} else if platformName == cypressPlatform {
		if caps.Image != "" {
			return caps.Image, nil
		}
		//use-case for task definition generation
		name := strings.ToLower(caps.BrowserName)
		name = remapName(name)
		version := strings.ToLower(caps.BrowserVersion)
		version = remapVersion(version)
		return imageRepo + name + ":" + version, nil
	} else {
		return "", fmt.Errorf("filed to build container image. unsupported platform specified. platformName=%s", caps.PlatformName)
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
