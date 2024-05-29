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

func manageTaskDefinitions() {
	definitionsCacheTtl := time.Hour * 13
	for {
		handlers.DefinitionRefreshDone = false

		log.Info("parsing images")
		images, err := images.ListImages()
		if err != nil {
			utils.ExitWithError(err, "failed to generate images", log.NewEntry(log.StandardLogger()))
		}

		log.Info("refreshing task definitions")
		err = definitions.RefreshTaskDefinitions(images, definitionsCacheTtl)
		if err != nil {
			utils.ExitWithError(err, "failed to refresh task definitions", log.NewEntry(log.StandardLogger()))
		}

		handlers.DefinitionRefreshDone = true

		log.Info("task definitions refresh finished")
		time.Sleep(definitionsCacheTtl - time.Hour)
	}
}

func CreateRouter() *gin.Engine {
	r := gin.New()

	r.GET("/", handlers.Ready)
	r.GET(definitions.IsReadyPath.String(), handlers.IsTaskDefinitionRefreshDone)
	r.GET(definitions.GetImagesPath.String(), handlers.GetImages)
	// TODO: create and implement handler
	r.GET(definitions.RefreshDefinitionsPath.String())

	return r
}

func main() {
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
	defer config.DbConnection.Close()

	err = config.InitCache()
	if err != nil {
		utils.ExitWithError(err, "Failed to init Redis client", log.NewEntry(log.StandardLogger()))
	}
	defer config.RedisMapperClient.Close()
	defer config.RedisDefinitionClient.Close()
	defer config.RedisResourcesClient.Close()
	defer config.RedisUtilityClient.Close()

	// create sigterm listener chan
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	// wrapping router by http.Server object and starting it in new thread to wait for quit chan signal
	srv := &http.Server{
		Addr:    definitions.E3SDefinitionsPort,
		Handler: CreateRouter(),
	}

	go func() {
		log.Infof("Listening on %s", definitions.E3SDefinitionsPort)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.WithError(err).Fatal("Failed to start e3s-definition server")
		}
	}()

	go manageTaskDefinitions()

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
