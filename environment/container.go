package environment

import (
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/aws/aws-sdk-go/service/ecs"
	log "github.com/sirupsen/logrus"
	"github.com/zebrunner/esg/capabilities"
	"github.com/zebrunner/esg/images"
)

var (
	capacityProviderResourcesLimit = make(map[string]smallestInstanceResources)
)

type smallestInstanceResources struct {
	res             Resources
	memoryDeviation int64
}

func AddSmallestInstanceResources(cpu int64, memory int64, capacityProvider string) {
	capacityProviderResourcesLimit[capacityProvider] = smallestInstanceResources{
		res:             Resources{Cpu: cpu, Memory: memory},
		memoryDeviation: getMemoryDeviation(memory),
	}
}

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
	image *images.Image

	Res Resources

	Essential  bool
	Privileged bool

	Ports            map[string]portMapping
	Mounts           []string // List of volume names
	Command          []string // Comma separated container startup command
	Env              envVariables
	EntryPoint       []string
	WorkingDirectory string

	HealthCheck *ecs.HealthCheck
	DependsOn   []*ecs.ContainerDependency

	ReadOnlyRootFileSystem bool
}

func (c *Container) Cpu() int64 {
	return c.Res.Cpu
}

func (c *Container) Memory() int64 {
	return c.Res.Memory
}

func (c Container) getImageUrl() string {
	if c.image == nil {
		return c.Image
	}

	return c.image.GetUrl()
}

func (c Container) getImageNameTag() string {
	if c.image == nil {
		return c.Image
	}

	return c.image.String()
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

type resourceCalculationHelper struct {
	MinimumRes Resources
	Container  *Container
	Memory     capabilities.Wrapper[int64]
	Cpu        capabilities.Wrapper[int64]
	wantedRes  Resources
}

func calculateResources(env *ExecutionEnvironment, resourcesArr ...*resourceCalculationHelper) error {
	for _, r := range resourcesArr {
		// Clear current container resources setting as it will be configured later
		r.Container.Res = Resources{0, 0}
	}

	resourcesLeft := Resources{0, 0}
	resLimit, ok := capacityProviderResourcesLimit[env.CapacityProvider]
	if !ok {
		resLimit = smallestInstanceResources{res: Resources{0, 0}}
		for _, r := range resourcesArr {
			resourcesLeft.Add(&r.MinimumRes)
		}
	} else {
		resourcesLeft = resLimit.res
		busyResources := SumResources(env.Containers)
		resourcesLeft.Remove(&busyResources)
		resourcesLeft.Remove(&Resources{0, resLimit.memoryDeviation})
	}

	totalWantedResources := Resources{0, 0}
	for _, r := range resourcesArr {
		isCpuOk, isMemoryOk := resourcesLeft.Compare(r.MinimumRes)
		if !isCpuOk || !isMemoryOk {
			r.Container.Res = Resources{-1, -1}
			return fmt.Errorf("environment is not supported with current min instance type")
		}

		wantedCpu := r.Cpu.ToPrimitive() - r.MinimumRes.Cpu
		if wantedCpu < 0 {
			log.WithFields(log.Fields{"wantedCpu": r.Cpu.ToPrimitive(), "minimumCpu": r.MinimumRes.Cpu, "container": r.Container.Name}).Trace("Increased requested cpu to min values")
			r.Cpu.From(r.MinimumRes.Cpu)
			wantedCpu = 0
		}

		wantedMemory := r.Memory.ToPrimitive() - r.MinimumRes.Memory
		if wantedMemory < 0 {
			log.WithFields(log.Fields{"wantedMemory": r.Memory.ToPrimitive(), "minimumMemory": r.MinimumRes.Memory, "container": r.Container.Name}).Trace("Increased requested memory to min values")
			r.Memory.From(r.MinimumRes.Memory)
			wantedMemory = 0
		}

		// wanted resources not including minimal values
		r.wantedRes = Resources{Cpu: wantedCpu, Memory: wantedMemory}
		totalWantedResources.Add(&r.wantedRes)

		resourcesLeft.Remove(&r.MinimumRes)
	}

	getExceedCoefficient := func(wantedTotal int64, freeTotal int64) float64 {
		// round up float 2 decimal (1.3333211 -> 1.34)
		exceedsMaximum := math.Ceil((float64(wantedTotal)/float64(freeTotal))*100) / 100
		if exceedsMaximum == 0 {
			exceedsMaximum = 0.01
		}

		return exceedsMaximum
	}

	cpuEnough, memoryEnough := resourcesLeft.Compare(totalWantedResources)
	if !cpuEnough {
		if resourcesLeft.Cpu == 0 {
			for _, r := range resourcesArr {
				log.WithFields(log.Fields{"wantedCpu": r.wantedRes.Cpu, "availiableCpu": r.MinimumRes.Cpu, "container": r.Container.Name}).Trace("Decreased requested cpu to min values")
				r.wantedRes.Cpu = 0
			}
		} else {
			cpuExceedsMaximum := getExceedCoefficient(totalWantedResources.Cpu, resourcesLeft.Cpu)
			for _, r := range resourcesArr {
				// decrease cpu in the same proportion for all conainers
				oldCpu := r.wantedRes.Cpu + r.MinimumRes.Cpu
				r.wantedRes.Cpu = int64(float64(r.wantedRes.Cpu) / cpuExceedsMaximum)
				log.WithFields(log.Fields{"wantedCpu": oldCpu, "availiableCpu": r.wantedRes.Cpu + r.MinimumRes.Cpu, "container": r.Container.Name}).Trace("Decreased requested cpu")
			}
		}
	}

	if !memoryEnough {
		if resourcesLeft.Memory == 0 {
			for _, r := range resourcesArr {
				log.WithFields(log.Fields{"wantedMemory": r.wantedRes.Memory, "availiableMemory": r.MinimumRes.Memory, "container": r.Container.Name}).Trace("Decreased requested memory to min values")
				r.wantedRes.Memory = 0
			}
		} else {
			memoryExceedsMaximum := getExceedCoefficient(totalWantedResources.Memory, resourcesLeft.Memory)
			for _, r := range resourcesArr {
				// decrease memory in the same proportion for all conainers
				oldMemory := r.wantedRes.Memory + r.MinimumRes.Memory
				r.wantedRes.Memory = int64(float64(r.wantedRes.Memory) / memoryExceedsMaximum)
				log.WithFields(log.Fields{"wantedMemory": oldMemory, "availiableMemory": r.wantedRes.Memory + r.MinimumRes.Memory, "container": r.Container.Name}).Trace("Decreased requested memory")
			}
		}
	}
	for _, r := range resourcesArr {
		r.Container.Res = Resources{
			Cpu:    r.MinimumRes.Cpu + r.wantedRes.Cpu,
			Memory: r.MinimumRes.Memory + r.wantedRes.Memory,
		}
		r.Cpu.From(r.Container.Res.Cpu)
		r.Memory.From(r.Container.Res.Memory)
	}

	return nil
}

func getMemoryDeviation(memory int64) int64 {
	basePercent := 2.8
	mbInGb := 1024
	// with bigger decreaseIndex bigger total memory deviation percent
	decreaseIndex := 16
	// deviationPercent is closer to basePercent with more memory on instance
	deviationPercent := (basePercent + float64(decreaseIndex)/(float64(memory)/float64(mbInGb)))
	return int64(math.Ceil(deviationPercent / 100 * float64(memory)))
}

func buildSchema(containers []*Container) string {
	namesArr := make([]string, 0)
	for _, container := range containers {
		namesArr = append(namesArr, container.Name)
	}
	sort.Strings(namesArr)

	return strings.Join(namesArr, "-")
}
