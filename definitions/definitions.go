package definitions

import (
	"context"
	"database/sql"
	"time"

	log "github.com/sirupsen/logrus"
	"github.com/zebrunner/esg/cachemaps/definitionmap"
	"github.com/zebrunner/esg/db"
	"github.com/zebrunner/esg/environment"

	"github.com/zebrunner/esg/images"
	"github.com/zebrunner/esg/service"
)

func RefreshTaskDefinitions(imagesArr []images.Image) error {
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

	err := definitionmap.WriteAll(hashRevisionMap)
	if err != nil {
		log.WithError(err).Error("Failed to add hashRevision map to redis")
		return err
	}

	return nil
}

func UpdateTaskDefinitions(imagesArr []images.Image) error {
	err := definitionmap.ExpireAll(time.Hour + 30*time.Minute)
	if err != nil {
		log.WithError(err).Error("Failed to add expire old task definitions")
		return err
	}

	return RefreshTaskDefinitions(imagesArr)
}

func compareWithStoredTaskDefinition(env *environment.ExecutionEnvironment) (*db.TaskDefinition, error) {
	l := log.WithField("schema", env.Schema).WithField("family", env.TaskDefinitionFamily)
	ctx := context.Background()

	newDbDefinititon := db.CreateTaskDefinitionEntity(env)
	savedDbDefinition, err := db.GetDefinition(env.TaskDefinitionFamily, env.Schema)
	if err != nil {
		if err != sql.ErrNoRows {
			return nil, err
		}

		l.Info("Creating new record")
		taskDef, err := service.CreateTaskDefinition(ctx, env.ContainerDefinitions(), env.Volume(), env.TaskDefinitionFamily, env.TaskRoleArn)
		if err != nil {
			return nil, err
		}
		// pause after aws call
		time.Sleep(1 * time.Second)
		newDbDefinititon.RevisionTag = int64(taskDef.Revision)

		err = db.InsertDefinition(newDbDefinititon)
		if err != nil {
			return nil, err
		}
	} else if newDbDefinititon.RegisterDefinitionHash != savedDbDefinition.RegisterDefinitionHash {
		l.Debug("Updating definition record")
		taskDef, err := service.CreateTaskDefinition(ctx, env.ContainerDefinitions(), env.Volume(), env.TaskDefinitionFamily, env.TaskRoleArn)
		if err != nil {
			return nil, err
		}
		// pause after aws call
		time.Sleep(1 * time.Second)
		newDbDefinititon.RevisionTag = int64(taskDef.Revision)

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
