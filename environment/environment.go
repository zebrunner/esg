package environment

import (
	"fmt"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/ecs"
	"github.com/google/uuid"
	"github.com/zebrunner/esg/cachemaps/definitionmap"
	"github.com/zebrunner/esg/cachemaps/resourcesToAllocate"
	"github.com/zebrunner/esg/capabilities"
	"github.com/zebrunner/esg/utils"
)

const (
	linuxPlatform   = "linux"
	androidPlatform = "android"
	redroidDevice   = "redroid"
	anyPlatform     = "any"
	genericPlatform = "generic"
	cypressPlatform = "cypress"
	windowsPlatform = "windows"

	//public zebrunner ECR docker registry
	imageRepo            = "public.ecr.aws/zebrunner/"
	uploaderImage        = imageRepo + "uploader:3.4"
	mitmImage            = imageRepo + "mitmproxy:1.2"
	recorderImage        = imageRepo + "recorder:1.5"
	cypressRecorderImage = imageRepo + "cypress-recorder:1.3"
	appiumImage          = imageRepo + "appium:2.0.5"
	cloneImage           = imageRepo + "git:2.36.2"
	entrypointImage      = imageRepo + "entrypoint:2.4"
	mavenImage           = imageRepo + "m2-repo-carina:1.5"
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
	minCpu      int64 = 128
	minMemory   int64 = 256
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
	Revision             int64
	Schema               string
	RouterUUID           string
	Containers           []*Container
	Capabilities         *capabilities.Capabilities
	ReqCapabilities      *capabilities.RequestCaps
	Volumes              map[string]volume
	Network              *NetworkConfiguration
	Workspace            string
}

func (e *ExecutionEnvironment) ContainerDefinitions() []*ecs.ContainerDefinition {
	definitions := []*ecs.ContainerDefinition{}

	for _, c := range e.Containers {
		cpu := c.Cpu()
		memory := c.Memory()
		definition := ecs.ContainerDefinition{
			Name:        &c.Name,
			Image:       &c.Image,
			Cpu:         &cpu,
			Memory:      &memory,
			Essential:   &c.Essential,
			HealthCheck: c.HealthCheck,
			DependsOn:   c.DependsOn,
		}

		if strings.ToLower(e.Capabilities.PlatformName.ToPrimitive()) != windowsPlatform {
			definition.MemoryReservation = &memory
			definition.Privileged = &c.Privileged
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
			Name:   &container.Name,
			Cpu:    &cpu,
			Memory: &memory,
		}

		if strings.ToLower(e.Capabilities.PlatformName.ToPrimitive()) != windowsPlatform {
			override.MemoryReservation = &memory
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

func (e *ExecutionEnvironment) HashOvverideDefinition() string {
	overrideContainersData := make([]*Container, 0)
	for _, container := range e.Containers {
		dependsOn := make([]*ecs.ContainerDependency, 0)
		if container.DependsOn != nil {
			for _, dependency := range container.DependsOn {
				if dependency == nil {
					continue
				}
				dependsOn = append(dependsOn, &ecs.ContainerDependency{
					Condition:     dependency.Condition,
					ContainerName: dependency.ContainerName,
				})
			}
		}

		var healthCheck *ecs.HealthCheck
		if container.HealthCheck != nil {
			healthCheck = &ecs.HealthCheck{
				Command:     container.HealthCheck.Command,
				Interval:    container.HealthCheck.Interval,
				Retries:     container.HealthCheck.Retries,
				StartPeriod: container.HealthCheck.StartPeriod,
				Timeout:     container.HealthCheck.Timeout,
			}
		}

		overrideContainersData = append(overrideContainersData, &Container{
			Name:             container.Name,
			Image:            container.Image,
			Essential:        container.Essential,
			Privileged:       container.Privileged,
			Ports:            container.Ports,
			Mounts:           container.Mounts,
			Links:            container.Links,
			EntryPoint:       container.EntryPoint,
			WorkingDirectory: container.WorkingDirectory,
			HealthCheck:      healthCheck,
			DependsOn:        dependsOn,
		})
	}

	overrideDefinitionData := &ExecutionEnvironment{
		TaskDefinitionFamily: e.TaskDefinitionFamily,
		Containers:           overrideContainersData,
	}

	overrideDefinitionHash := utils.EncodeToHash(overrideDefinitionData)

	return overrideDefinitionHash
}

func (e *ExecutionEnvironment) HashRegisterDefinition() string {
	containers := make([]*Container, 0)
	for _, container := range e.Containers {
		dependsOn := make([]*ecs.ContainerDependency, 0)
		if container.DependsOn != nil {
			for _, dependency := range container.DependsOn {
				if dependency == nil {
					continue
				}
				dependsOn = append(dependsOn, &ecs.ContainerDependency{
					Condition:     dependency.Condition,
					ContainerName: dependency.ContainerName,
				})
			}
		}

		var healthCheck *ecs.HealthCheck
		if container.HealthCheck != nil {
			healthCheck = &ecs.HealthCheck{
				Command:     container.HealthCheck.Command,
				Interval:    container.HealthCheck.Interval,
				Retries:     container.HealthCheck.Retries,
				StartPeriod: container.HealthCheck.StartPeriod,
				Timeout:     container.HealthCheck.Timeout,
			}
		}

		containers = append(containers, &Container{
			Name:             container.Name,
			Image:            container.Image,
			cpu:              container.cpu,
			memory:           container.memory,
			Essential:        container.Essential,
			Privileged:       container.Privileged,
			Ports:            container.Ports,
			Mounts:           container.Mounts,
			Links:            container.Links,
			Command:          container.Command,
			Env:              container.Env,
			EntryPoint:       container.EntryPoint,
			WorkingDirectory: container.WorkingDirectory,
			HealthCheck:      healthCheck,
			DependsOn:        dependsOn,
		})
	}

	registerDefinitionData := &ExecutionEnvironment{
		TaskDefinitionFamily: e.TaskDefinitionFamily,
		Containers:           containers,
		Volumes:              e.Volumes,
		Network:              e.Network,
	}
	registerDefinitionHash := utils.EncodeToHash(registerDefinitionData)

	return registerDefinitionHash
}

func (env *ExecutionEnvironment) CalculateResources() *resourcesToAllocate.ResourcesToAllocate {
	var cpu int64 = 0
	var memory int64 = 0

	for _, container := range env.Containers {
		cpu += container.cpu
		memory += container.memory
	}

	resources := resourcesToAllocate.ResourcesToAllocate{
		RouterUUID: env.RouterUUID,
		Cpu:        cpu,
		Memory:     memory,
	}

	return &resources
}

func (env *ExecutionEnvironment) GetFamilyRevision() (string, error) {
	//used Contains() as task definition family could be org-generic/dev-generic etc.
	if strings.Contains(env.TaskDefinitionFamily, "generic") {
		return env.TaskDefinitionFamily, nil
	}

	revision, found := definitionmap.FindRevision(env.HashOvverideDefinition())
	if !found {
		return "", fmt.Errorf("revision not found for '%s'", env.TaskDefinitionFamily)
	}

	return fmt.Sprint(env.TaskDefinitionFamily, ":", revision), nil
}

// build's new ExecutionEnvironment env with new router uuid
func Build(workspace string, caps *capabilities.Capabilities) (*ExecutionEnvironment, error) {
	return build(workspace, uuid.NewString(), caps)
}

func BuildFromCaps(caps *capabilities.Capabilities) (*ExecutionEnvironment, error) {
	return build("", "", caps)
}

func build(workspace string, routerUUID string, caps *capabilities.Capabilities) (*ExecutionEnvironment, error) {
	platform := strings.ToLower(caps.PlatformName.ToPrimitive())
	if platform == androidPlatform {
		if strings.ToLower(caps.DeviceName.ToPrimitive()) == redroidDevice {
			return buildAppiumRedroid(workspace, routerUUID, caps)
		}
		return nil, fmt.Errorf("device is not supported. deviceName=%s", caps.DeviceName)
	} else if platform == genericPlatform {
		return buildGeneric(workspace, routerUUID, caps)
	} else if platform == cypressPlatform {
		return buildCypress(workspace, routerUUID, caps)
	} else if platform == windowsPlatform {
		return buildWindowsBrowser(workspace, routerUUID, caps)
	} else if platform == linuxPlatform || platform == "" || platform == anyPlatform {
		return buildBrowser(workspace, routerUUID, caps)
	}

	return nil, fmt.Errorf("platform is not supported. platformName=%s", caps.PlatformName)
}

func buildSchema(Containers []*Container) string {
	namesArr := make([]string, 0)
	for _, container := range Containers {
		namesArr = append(namesArr, container.Name)
	}
	sort.Strings(namesArr)

	return strings.Join(namesArr, "-")
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
	platformName := strings.ToLower(caps.PlatformName.ToPrimitive())
	deviceName := strings.ToLower(caps.DeviceName.ToPrimitive())

	if platformName == anyPlatform || platformName == "" {
		platformName = "linux"
	}

	if platformName == androidPlatform && deviceName == redroidDevice {
		name := redroidDevice
		version := caps.PlatformVersion
		if version == "" {
			version = "latest"
		}
		return imageRepo + name + ":" + version.ToPrimitive(), nil
	} else if platformName == linuxPlatform {
		name := strings.ToLower(caps.BrowserName.ToPrimitive())
		name = remapName(name)
		version := strings.ToLower(caps.BrowserVersion.ToPrimitive())
		version = remapVersion(version)
		return imageRepo + name + ":" + version, nil
	} else if platformName == cypressPlatform {
		if caps.Image != "" {
			return caps.Image.ToPrimitive(), nil
		}
		//use-case for task definition generation
		name := strings.ToLower(caps.BrowserName.ToPrimitive())
		name = remapName(name)
		version := strings.ToLower(caps.BrowserVersion.ToPrimitive())
		version = remapVersion(version)
		return imageRepo + name + ":" + version, nil
	} else if platformName == windowsPlatform {
		name := strings.ToLower(caps.BrowserName.ToPrimitive())
		name = remapName(name)
		version := strings.ToLower(caps.BrowserVersion.ToPrimitive())
		version = remapVersion(version)
		return imageRepo + windowsPlatform + "-" + name + ":" + version, nil
	} else {
		return "", fmt.Errorf("failed to build container image. unsupported platform specified. platformName=%s", caps.PlatformName)
	}
}

func buildTaskDefinitionFamily(caps *capabilities.Capabilities) string {
	familyParts := []string{}

	if zbrEnv := os.Getenv("ZEBRUNNER_ENV"); zbrEnv != "" {
		familyParts = append(familyParts, zbrEnv)
	}

	platformName := strings.ToLower(caps.PlatformName.ToPrimitive())

	if caps.PlatformName == "" || platformName == "any" {
		platformName = "linux"
	}

	familyParts = append(familyParts, platformName)

	deviceName := strings.ToLower(caps.DeviceName.ToPrimitive())
	if deviceName != "" {
		familyParts = append(familyParts, deviceName)
		if deviceName == "redroid" {
			platformVersion := strings.ToLower(caps.PlatformVersion.ToPrimitive())
			platformVersion = remapVersion(platformVersion)
			platformVersion = strings.Replace(platformVersion, ".", "-", -1)
			familyParts = append(familyParts, platformVersion)
		}
	}

	browserName := strings.ToLower(caps.BrowserName.ToPrimitive())
	if browserName != "" && deviceName != "redroid" {
		familyParts = append(familyParts, remapName(browserName))
		browserVersion := strings.ToLower(caps.BrowserVersion.ToPrimitive())
		browserVersion = remapVersion(browserVersion)
		browserVersion = strings.Replace(browserVersion, ".", "-", -1)
		familyParts = append(familyParts, browserVersion)
	}

	taskDefFamily := strings.Join(familyParts, "-")

	return taskDefFamily
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
