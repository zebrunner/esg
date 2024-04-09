package environment

import (
	"fmt"
	"math"

	"github.com/aws/aws-sdk-go/service/ecs"
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

func (c *Container) CalculateResource(minRes Resources, capasityProviderName string, caps *capabilities.Capabilities, otherContainers []*Container) error {
	maxRes, ok := CapacityProvdirResourcesLimit[capasityProviderName]
	if !ok {
		maxRes = minRes
	} else {
		busyResources := SumResources(otherContainers)
		maxRes.Remove(&busyResources)
		maxRes.Remove(&Resources{0, memoryDeviation})
	}

	cpu, err := applyConstraints(caps.Cpu.ToPrimitive(), minRes.Cpu, maxRes.Cpu)
	if err != nil {
		return err
	}

	c.Res.Cpu = cpu
	caps.Cpu.From(cpu)

	memory, err := applyConstraints(caps.Memory.ToPrimitive(), minRes.Memory, maxRes.Memory)
	if err != nil {
		return err
	}

	c.Res.Memory = memory
	caps.Memory.From(memory)

	return nil
}

func applyConstraints(desiredAmount int64, min int64, max int64) (int64, error) {
	resource := min
	if desiredAmount > resource {
		resource = desiredAmount
	}

	if resource > max {
		resource = max
	}

	// If max is smaller than min value -> env is not supported with current min instance type
	if max < min {
		return -1, fmt.Errorf("environment is not supported with current min instance type")
	}

	return resource, nil
}

type resourceCalculatorHelper struct {
	MinimumRes Resources
	Container  *Container
	Memory     capabilities.Wrapper[int64]
	Cpu        capabilities.Wrapper[int64]
	wantedRes  Resources
}

func calculateResourcesForSeveralContainers(env *ExecutionEnvironment, resourcesArr ...resourceCalculatorHelper) error {
	freeResource, ok := CapacityProvdirResourcesLimit[env.CapacityProvider]
	if !ok {
		for _, r := range resourcesArr {
			r.Container.Res = r.MinimumRes
		}

		return nil
	}

	for _, r := range resourcesArr {
		// Clear current container resources setting as it will be configured later
		r.Container.Res = Resources{0, 0}
	}

	busyResources := SumResources(env.Containers)
	freeResource.Remove(&busyResources)
	freeResource.Remove(&Resources{0, memoryDeviation})

	totalWantedResources := Resources{0, 0}
	for _, r := range resourcesArr {
		wantedCpu := r.Cpu.ToPrimitive() - r.MinimumRes.Cpu
		if wantedCpu < 0 {
			wantedCpu = 0
		}

		wantedMemory := r.Memory.ToPrimitive() - r.MinimumRes.Memory
		if wantedMemory < 0 {
			wantedMemory = 0
		}

		// wanted resources not including minimal values
		r.wantedRes = Resources{Cpu: wantedCpu, Memory: wantedMemory}
		totalWantedResources.Add(&r.wantedRes)

		isCpuOk, isMemoryOk := freeResource.Compare(r.MinimumRes)
		if !isCpuOk || !isMemoryOk {
			r.Container.Res = Resources{-1, -1}
			return fmt.Errorf("environment is not supported with current min instance type")
		}

		freeResource.Remove(&r.MinimumRes)
	}

	getExceedCoefficient := func(wantedTotal int64, freeTotal int64) float64 {
		// round up float 2 decimal (1.3333211 -> 1.34)
		exceedsMaximum := math.Ceil((float64(wantedTotal)/float64(freeTotal))*100) / 100
		if exceedsMaximum == 0 {
			exceedsMaximum = 0.01
		}

		return exceedsMaximum
	}

	cpuEnough, memoryEnough := freeResource.Compare(totalWantedResources)
	if !cpuEnough {
		cpuExceedsMaximum := getExceedCoefficient(totalWantedResources.Cpu, freeResource.Cpu)
		for _, r := range resourcesArr {
			// decrease cpu in the same proportion for all conainers
			r.wantedRes.Cpu = int64(float64(r.wantedRes.Cpu) / cpuExceedsMaximum)
		}
	}

	if !memoryEnough {
		memoryExceedsMaximum := getExceedCoefficient(totalWantedResources.Memory, freeResource.Memory)
		for _, r := range resourcesArr {
			// decrease cpu in the same proportion for all conainers
			r.wantedRes.Cpu = int64(float64(r.wantedRes.Cpu) / memoryExceedsMaximum)
		}
	}

	for _, r := range resourcesArr {
		r.Container.Res = Resources{Cpu: r.MinimumRes.Cpu + r.wantedRes.Cpu, Memory: r.MinimumRes.Memory + r.wantedRes.Memory}
		r.Cpu.From(r.Container.Res.Cpu)
		r.Memory.From(r.Container.Res.Memory)
	}

	return nil
}
