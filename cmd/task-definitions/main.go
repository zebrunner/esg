package main

import (
	"context"
	"flag"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	log "github.com/sirupsen/logrus"
	"github.com/zebrunner/esg/cachemaps/utilsmap"
	"github.com/zebrunner/esg/config"
	"github.com/zebrunner/esg/definitions"
	"github.com/zebrunner/esg/handlers"
	"github.com/zebrunner/esg/images"
	"github.com/zebrunner/esg/service"
	"github.com/zebrunner/esg/utils"
)

const (
	listen = ":5555"
)

func environmentRefresh() error {
	handlers.DefinitionRefreshDone = false
	log.Info("Parsing images")
	images, err := images.ListImages(config.Conf.ImageRepositories, config.Conf.ExcludeBrowsers)
	if err != nil {
		log.WithError(err).Error("failed to generate images")
		return err
	}

	log.Info("Refreshing task definitions")
	err = definitions.RefreshTaskDefinitions(images)
	if err != nil {
		log.WithError(err).Error("failed to refresh task definitions")
		return err
	}

	log.Info("Task definitions refresh finished")
	handlers.DefinitionRefreshDone = true

	return nil
}

func environmentUpdate() error {
	log.Info("Parsing images")
	images, err := images.ListImages(config.Conf.ImageRepositories, config.Conf.ExcludeBrowsers)
	if err != nil {
		log.WithError(err).Error("failed to generate images")
		return err
	}

	log.Info("Updating task definitions")
	err = definitions.UpdateTaskDefinitions(images)
	if err != nil {
		log.WithError(err).Error("failed to refresh task definitions")
		return err
	}

	log.Info("Task definitions update finished")

	return nil
}

func startEnvironmentUpdate() error {
	err := environmentRefresh()
	if err != nil {
		return err
	}

	envUpdateInterval := config.Conf.TaskDefinitionsUpdateInterval
	go func() {
		time.Sleep(envUpdateInterval)

		for {
			err := environmentUpdate()
			if err != nil {
				log.WithError(err).Warn("Failed to update task definitions. Retrying...")
				time.Sleep(time.Second * 15)
			} else {
				time.Sleep(envUpdateInterval)
			}
		}
	}()

	return nil
}

func CreateRouter() *gin.Engine {
	r := gin.New()

	r.GET("/", handlers.Ready)
	r.GET(definitions.IsReadyPath.String(), handlers.IsTaskDefinitionRefreshDone)
	r.GET(definitions.GetImagesPath.String(), handlers.GetImages)
	r.POST(definitions.RefreshDefinitionsPath.String(), handlers.RefreshDefinitions)

	return r
}

func main() {
	defer func() {
		config.CloseConnections()
	}()

	flag.Parse()

	log.SetLevel(config.Conf.ParseLogLevel())
	awsSess, err := service.InitAws()
	if err != nil {
		utils.ExitWithError(err, "Failed to init aws session", log.NewEntry(log.StandardLogger()))
	}
	service.AwsSess = awsSess

	err = config.InitDBConnection(config.Conf.DbConnectionString)
	if err != nil {
		utils.ExitWithError(err, "Failed to init DB client", log.NewEntry(log.StandardLogger()))
	}

	err = config.InitRedisClusterConnection()
	if err != nil {
		utils.ExitWithError(err, "Failed to init redis connection", log.NewEntry(log.StandardLogger()))
	}

	err = utilsmap.TaskDefinitionsVersion.Set(config.Version)
	if err != nil {
		log.WithError(err).Error("Failed to set task-definitions version in cache")
	}

	// create sigterm listener chan
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	// wrapping router by http.Server object and starting it in new thread to wait for quit chan signal
	srv := &http.Server{
		Addr:    listen,
		Handler: CreateRouter(),
	}

	go func() {
		log.Infof("Listening on %s", listen)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.WithError(err).Fatal("Failed to start task-definitions server")
		}
	}()

	err = startEnvironmentUpdate()
	if err != nil {
		utils.ExitWithError(err, "Failed to refresh task definitions", log.NewEntry(log.StandardLogger()))
	}

	log.Info("Service started")

	<-quit

	log.Info("Shutdown task-definitions ...")

	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()

	if err := srv.Shutdown(ctx); err != nil {
		log.WithError(err).Error("Failed to shutdown correctly")
	}

	log.Info("task-definitions exited")
}
