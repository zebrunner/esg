package resourcesToAllocate

import (
	"fmt"

	"github.com/zebrunner/esg/cachemaps"
	"github.com/zebrunner/esg/config"
)

type ResourcesToAllocate struct {
	RouterUUID       string
	Cpu              int64
	Memory           int64
	CapacityProvider string
}

func (rsa *ResourcesToAllocate) generateRedisId() string {
	return fmt.Sprintf("%s_%s", rsa.CapacityProvider, rsa.RouterUUID)
}

func GetEntitiesOfCapacityProvider(capacityProvider string) ([]*ResourcesToAllocate, error) {
	keys, err := cachemaps.GetKeys(cachemaps.UNALLOCATED_RESOURCES)
	if err != nil {
		return nil, err
	}

	resources, err := cachemaps.FindAll[*ResourcesToAllocate](config.RedisCluster.Pipeline(), keys)
	if err != nil {
		return nil, err
	}

	resourcesOfCapacityProvider := make([]*ResourcesToAllocate, 0)
	for _, resource := range resources {
		if resource != nil && resource.CapacityProvider == capacityProvider {
			resourcesOfCapacityProvider = append(resourcesOfCapacityProvider, resource)
		}
	}

	return resourcesOfCapacityProvider, nil
}
