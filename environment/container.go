package environment

import (
	"github.com/aws/aws-sdk-go/service/ecs"
)

type envVariables = map[string]string

type volume struct {
	HostPath      string
	ContainerPath string
	Driver        string
	Scope         string
	ReadOnly      bool
}

type portMapping struct {
	ContainerPort int64
	HostPort      int64
}

type Container struct {
	Name   string
	Image  string
	cpu    int64
	memory int64

	Essential  bool
	Privileged bool

	Ports            map[string]portMapping
	Mounts           []string // List of volume names
	Links            []string // List of linked containers
	Command          []string // Comma separated container startup command
	Env              envVariables
	EntryPoint       []string
	WorkingDirectory string

	HealthCheck *ecs.HealthCheck
	DependsOn   []*ecs.ContainerDependency
}

func (c *Container) Cpu() int64 {
	return c.cpu
}

func (c *Container) SetCpu(capsCpu *int64, minCpu int64, maxCpu int64) {
	c.cpu = calculateResource(*capsCpu, minCpu, maxCpu)
	*capsCpu = c.cpu //override default one as we have min/max limits
}

func (c *Container) Memory() int64 {
	return c.memory
}

func (c *Container) SetMemory(capsMemory *int64, minMemory int64, maxMemory int64) {
	c.memory = calculateResource(*capsMemory, minMemory, maxMemory)
	*capsMemory = c.memory //override default one as we have min/max limits
}

func calculateResource(amount int64, min int64, max int64) int64 {
	resource := min
	if amount > resource {
		resource = amount
	}

	if resource > max {
		resource = max
	}

	return resource
}
