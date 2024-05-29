package definitions

import (
	"database/sql"
	"time"

	log "github.com/sirupsen/logrus"
	"github.com/zebrunner/esg/cachemaps/definitionmap"
	"github.com/zebrunner/esg/db"
	"github.com/zebrunner/esg/environment"
	"github.com/zebrunner/esg/environment/builder"
	"github.com/zebrunner/esg/images"
	"github.com/zebrunner/esg/service"
)

func RefreshTaskDefinitions(imagesArr []images.Image, taskDefinitionCacheTtl time.Duration) error {
	hashRevisionMap := make(map[string]int64)
	for _, img := range imagesArr {
		envsList, err := buildEnvsFromImage(img)
		if err != nil {
			log.WithError(err).Error("Failed to build execution environments from images list")
			return err
		}
		for _, env := range envsList {
			dbTaskDefinition, err := compareWithStoredTaskDefinition(env)
			if err != nil {
				log.WithError(err).WithField("family", env.TaskDefinitionFamily).Error("Couldn't create task defenition")
				return err
			}

			hashRevisionMap[dbTaskDefinition.OverrideDefinitionHash] = dbTaskDefinition.RevisionTag
		}
	}

	err := definitionmap.WriteAll(hashRevisionMap, taskDefinitionCacheTtl)
	if err != nil {
		log.WithError(err).Error("Failed to add hashRevision map to redis")
		return err
	}

	return nil
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
		taskDef, err := service.CreateTaskDefinition(env)
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
		l.Info("Updating definition record")
		taskDef, err := service.CreateTaskDefinition(env)
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

func buildEnvsFromImage(image images.Image) ([]*environment.ExecutionEnvironment, error) {
	l := log.WithField("image", image.ToString())

	capsList, err := image.GetMockCapabilities()
	if err != nil {
		l.WithError(err).Error("Failed to build capabilitites from image!")
		return nil, err
	}

	envsList := make([]*environment.ExecutionEnvironment, 0)
	for _, caps := range capsList {
		env, err := builder.BuildEnvForTaskDefinitionGeneration(image, caps)
		if err != nil {
			l.WithError(err).Error("Failed to build execution environment")
			return nil, err
		}

		envsList = append(envsList, env)
	}

	return envsList, nil
}
