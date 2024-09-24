package cache

import (
	"database/sql"
	"sync"
	"time"

	log "github.com/sirupsen/logrus"
	"github.com/zebrunner/esg/cachemaps"
	"github.com/zebrunner/esg/config"
	"github.com/zebrunner/esg/db"
	"github.com/zebrunner/esg/environment"
	"github.com/zebrunner/esg/images"
	"github.com/zebrunner/esg/service"
)

var (
	cache       = &RevisionsCache{}
	refreshDone = &RefreshDone{}
)

type RevisionsCache struct {
	sync.RWMutex
	Cache map[string]int64
}

type RefreshDone struct {
	sync.Mutex
	DefinitionRefreshDone bool
}

type hashRevision struct {
	Hash     string
	Revision int64
}

func GetCache() *RevisionsCache {
	return cache
}

func Refresh() error {
	refreshDone.Lock()
	refreshDone.DefinitionRefreshDone = false
	refreshDone.Unlock()

	images, err := images.ListImages(config.Conf.ImageRepositories, config.Conf.ExcludeBrowsers)
	if err != nil {
		log.WithError(err).Error("failed to generate images")
		return err
	}

	revisions := make(map[string]int64)
	for _, img := range images {
		envs, err := buildEnvs(img)
		if err != nil {
			log.WithError(err).Error("Failed to build execution environments from images list")
			return err
		}
		for _, env := range envs {
			dbTaskDefinition, err := compareWithStoredTaskDefinition(env)
			if err != nil {
				log.WithError(err).WithField("family", env.TaskDefinitionFamily).Error("Couldn't create task defenition")
				return err
			}
			revisions[dbTaskDefinition.OverrideDefinitionHash] = dbTaskDefinition.RevisionTag
		}
	}

	if err := WriteAll(revisions); err != nil {
		log.WithError(err).Error("Failed to add hashRevision map to redis")
		return err
	}

	refreshDone.Lock()
	refreshDone.DefinitionRefreshDone = true
	refreshDone.Unlock()
	return nil
}

// Add new revisions
func WriteAll(definitions map[string]int64) error {
	hashRevisionMap := make(map[string]hashRevision, len(definitions))
	for k, v := range definitions {
		hashRevisionMap[k] = hashRevision{Hash: k, Revision: v}
	}

	return cachemaps.WriteAll(config.RedisCluster.Pipeline(), cachemaps.DEFINITION, hashRevisionMap)
}

func compareWithStoredTaskDefinition(env *environment.ExecutionEnvironment) (*db.TaskDefinition, error) {
	l := log.WithField("schema", env.Schema).WithField("family", env.TaskDefinitionFamily)

	newDbDefinititon := db.CreateTaskDefinitionEntity(env)
	savedDbDefinition, err := db.GetDefinition(env.TaskDefinitionFamily, env.Schema)
	if err != nil {
		if err != sql.ErrNoRows {
			return nil, err
		}

		l.Info("Creating new record")
		taskDef, err := service.CreateTaskDefinition(env.ContainerDefinitions(), env.Volume(), env.TaskDefinitionFamily, env.TaskRoleArn)
		if err != nil {
			return nil, err
		}
		// pause after aws call
		time.Sleep(1 * time.Second)
		newDbDefinititon.RevisionTag = *taskDef.Revision

		err = db.InsertDefinition(newDbDefinititon)
		if err != nil {
			return nil, err
		}
	} else if newDbDefinititon.RegisterDefinitionHash != savedDbDefinition.RegisterDefinitionHash {
		l.Debug("Updating definition record")
		taskDef, err := service.CreateTaskDefinition(env.ContainerDefinitions(), env.Volume(), env.TaskDefinitionFamily, env.TaskRoleArn)
		if err != nil {
			return nil, err
		}
		// pause after aws call
		time.Sleep(1 * time.Second)
		newDbDefinititon.RevisionTag = *taskDef.Revision

		err = db.RefreshTag(savedDbDefinition.RegisterDefinitionHash, newDbDefinititon)
		if err != nil {
			return nil, err
		}
	} else {
		l.Trace("Definition record is up-to-date")
		newDbDefinititon.RevisionTag = savedDbDefinition.RevisionTag
	}

	return newDbDefinititon, nil
}

func buildEnvs(image images.Image) ([]*environment.ExecutionEnvironment, error) {
	l := log.WithField("image", image.String())

	capsList, err := image.GetMockCapabilities()
	if err != nil {
		l.WithError(err).Error("Failed to build capabilitites from image!")
		return nil, err
	}

	envsList := make([]*environment.ExecutionEnvironment, 0)
	for _, caps := range capsList {
		env, err := environment.BuildEnvForTaskDefinitionGeneration(image, caps)
		if err != nil {
			l.WithError(err).Error("Failed to build execution environment")
			return nil, err
		}

		envsList = append(envsList, env)
	}
	return envsList, nil
}
