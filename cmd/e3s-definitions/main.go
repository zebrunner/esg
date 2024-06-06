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

func taskDefinitionsUpdate(cacheTtl time.Duration) error {
	handlers.DefinitionRefreshDone = false
	log.Info("Parsing images")
	images, err := images.ListImages(config.Conf.ImageRepositories, config.Conf.ExcludeBrowsers)
	if err != nil {
		log.WithError(err).Error("failed to generate images")
		return err
	}

	log.Info("Refreshing task definitions")
	err = definitions.RefreshTaskDefinitions(images, cacheTtl)
	if err != nil {
		log.WithError(err).Error("failed to refresh task definitions")
		return err
	}

	log.Info("Task definitions refresh finished")
	handlers.DefinitionRefreshDone = true
	return nil
}

func startTaskDefinitionsUpdate() error {
	definitionsCacheTtl := time.Hour * 13
	// utils.ExitWithError(err, "failed to generate images", log.NewEntry(log.StandardLogger()))
	err := taskDefinitionsUpdate(definitionsCacheTtl)
	if err != nil {
		return err
	}

	go func() {
		time.Sleep(definitionsCacheTtl - time.Hour)

		for {
			err := taskDefinitionsUpdate(definitionsCacheTtl)
			if err != nil {
				retrySleep := time.Second * 15
				log.WithError(err).Warnf("Failed to update task definitions. Retrying in %v seconds", retrySleep.Seconds())
				time.Sleep(retrySleep)
			} else {
				time.Sleep(definitionsCacheTtl)
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

	err = config.REDIS_MAPPER_CLIENT.InitConnection()
	if err != nil {
		utils.ExitWithError(err, "Failed to init redis connection", log.NewEntry(log.StandardLogger()))
	}

	err = config.REDIS_DEFINITIONS_CLIENT.InitConnection()
	if err != nil {
		utils.ExitWithError(err, "Failed to init redis connection", log.NewEntry(log.StandardLogger()))
	}

	err = config.REDIS_UTILITY_CLIENT.InitConnection()
	if err != nil {
		utils.ExitWithError(err, "Failed to init redis connection", log.NewEntry(log.StandardLogger()))
	}

	err = config.REDIS_RESOURCES_CLIENT.InitConnection()
	if err != nil {
		utils.ExitWithError(err, "Failed to init redis connection", log.NewEntry(log.StandardLogger()))
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
			log.WithError(err).Fatal("Failed to start e3s-definition server")
		}
	}()

	err = startTaskDefinitionsUpdate()
	if err != nil {
		utils.ExitWithError(err, "Failed to refresh task definitions", log.NewEntry(log.StandardLogger()))
	}

	log.Info("Service started")

	<-quit

	log.Info("Shutdown e3s-definition ...")

	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()

	if err := srv.Shutdown(ctx); err != nil {
		log.WithError(err).Error("Failed to shutdown correctly")
	}

	log.Info("e3s-definition exited")
}
