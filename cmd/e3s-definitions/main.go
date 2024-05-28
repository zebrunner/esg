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
	"github.com/zebrunner/esg/environment"
	"github.com/zebrunner/esg/handlers"
	"github.com/zebrunner/esg/service"
	"github.com/zebrunner/esg/utils"
)

const listen = ":5555"

var (
	refreshDone = false
)

func CreateRouter() *gin.Engine {
	r := gin.New()

	r.GET("/ready", handlers.IsTaskDefinitionRefreshDone)
	r.GET("/execution-environment", handlers.BuildExecutionEnvironment)
	r.GET("/images", handlers.GetImages)

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

	// create sigterm listener chan
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	scalersMap, err := service.InitScalingData()
	if err != nil {
		utils.ExitWithError(err, "Failed to init scaling data", log.NewEntry(log.StandardLogger()))
	}

	for capacityProvider, scaler := range scalersMap {
		environment.AddSmallestInstanceResources(scaler.InstanceResources.CPU, scaler.InstanceResources.Memory, capacityProvider)
	}

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
