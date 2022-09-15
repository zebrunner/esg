package environment

import (
	"github.com/aws/aws-sdk-go/service/ecs"
	"github.com/zebrunner/esg/config"
)

type envVariables = map[string]string

type volume struct {
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
	Mounts      []string // List of names of volumes
	Links       []string // List of linked containers
	Command     []string //Comma separate container startup command
	Env         envVariables
	EntryPoint []string
	WorkingDirectory string

        HealthCheck *ecs.HealthCheck
	DependsOn []*ecs.ContainerDependency
}

func (c *Container) Cpu() int64 {
	return c.cpu
}

func (c *Container) SetCpu(amount int64) {
	c.cpu = calculateResource(amount, config.Conf.MinCpu, config.Conf.MaxCpu)
}

func (c *Container) Memory() int64 {
	return c.memory
}

func (c *Container) SetMemory(amount int64) {
	c.memory = calculateResource(amount, config.Conf.MinMemory, config.Conf.MaxMemory)
}

func (c *Container) MemoryReservation() int64 {
	return c.memoryReservation
}

func (c *Container) SetMemoryReservation(amount int64) {
	c.memoryReservation = calculateResource(amount, config.Conf.MinMemoryReservation, config.Conf.MaxMemoryReservation)
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
