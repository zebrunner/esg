package environment

import (
	"github.com/aws/aws-sdk-go/service/ecs"
	log "github.com/sirupsen/logrus"
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

type Resources struct {
	Cpu    int64
	Memory int64
}

type Container struct {
	Name  string
	Image string

	Res Resources

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
	return c.Res.Cpu
}

func (c *Container) Memory() int64 {
	return c.Res.Memory
}

func (r Resources) Compare(res Resources) (cpuBool bool, memoryBool bool) {
	return r.Cpu >= res.Cpu, r.Memory >= res.Memory
}

func (r *Resources) Add(res *Resources) {
	r.Cpu += res.Cpu
	r.Memory += res.Memory
}

func (r *Resources) Remove(res *Resources) {
	r.Cpu -= res.Cpu
	r.Memory -= res.Memory
}

func SumResources(containers []*Container) Resources {
	resources := Resources{
		Cpu:    0,
		Memory: 0,
	}

	for _, container := range containers {
		if container != nil {
			resources.Add(&container.Res)
		}
	}

	return resources
}

func (c *Container) CalculateResource(minRes Resources, capasityProviderName string, caps *capabilities.Capabilities, otherContainers []*Container) {
	maxRes, ok := CapacityProvdirResourcesLimit[capasityProviderName]
	if !ok {
		maxRes = minRes
	} else {
		busyResources := SumResources(otherContainers)
		maxRes.Remove(&busyResources)
	}

	cpu := applyConstraints(caps.Cpu.ToPrimitive(), minRes.Cpu, maxRes.Cpu)
	c.Res.Cpu = cpu
	caps.Cpu.From(cpu)

	memory := applyConstraints(caps.Memory.ToPrimitive(), minRes.Memory, maxRes.Memory)
	c.Res.Memory = memory
	caps.Memory.From(memory)

	log.WithFields(log.Fields{"conainerName": c.Name, "memory": memory, "cpu": cpu}).Info("Calculated resources for container")
}

func applyConstraints(desiredAmount int64, min int64, max int64) int64 {
	resource := min
	if desiredAmount > resource {
		resource = desiredAmount
	}

	if resource > max {
		resource = max
	}

	// If max is smaller than min value -> env is not supported with current min instance type
	if max < min {
		resource = -1
	}

	return resource
}
