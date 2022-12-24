package environment

import (
	"github.com/aws/aws-sdk-go/service/ecs"
	"github.com/zebrunner/esg/config"
        "github.com/zebrunner/esg/capabilities"
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
	Name              string
	Image             string
	cpu               int64
	memory            int64
	memoryReservation int64

	Essential  bool
	Privileged bool

	Ports       map[string]portMapping
	Mounts  []string // List of volume names
	Links       []string // List of linked containers
	Command     []string // Comma separated container startup command
	Env         envVariables
	EntryPoint []string
	WorkingDirectory string

        HealthCheck *ecs.HealthCheck
	DependsOn []*ecs.ContainerDependency
}

func (c *Container) Cpu() int64 {
	return c.cpu
}

func (c *Container) SetCpu(caps *capabilities.Capabilities) {
	c.cpu = calculateResource(caps.Cpu, config.Conf.MinCpu, config.Conf.MaxCpu)
        caps.Cpu = c.cpu //override default one as we have min/max limits
}

func (c *Container) Memory() int64 {
	return c.memory
}

func (c *Container) SetMemory(caps *capabilities.Capabilities) {
	c.memory = calculateResource(caps.Memory, config.Conf.MinMemory, config.Conf.MaxMemory)
        caps.Memory = c.memory //override default one as we have min/max limits
}

func (c *Container) MemoryReservation() int64 {
	return c.memoryReservation
}

func (c *Container) SetMemoryReservation(caps *capabilities.Capabilities) {
	c.memoryReservation = calculateResource(caps.MemoryReservation, config.Conf.MinMemoryReservation, config.Conf.MaxMemoryReservation)
        caps.MemoryReservation = c.memoryReservation //override default one as we have min/max limits
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
