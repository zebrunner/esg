package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net"
	"os"
	"os/signal"
	"syscall"

	log "github.com/sirupsen/logrus"
	"github.com/zebrunner/esg/config"
	"github.com/zebrunner/esg/environment"
	"github.com/zebrunner/esg/service"
	"github.com/zebrunner/esg/task-definitions-service/cache"
	"github.com/zebrunner/esg/task-definitions-service/definitions"
	"github.com/zebrunner/esg/utils"
	"google.golang.org/grpc"
)

const (
	listen = ":5555"
)

type ServiceServerImpl struct {
	definitions.ServiceServer
}

func (ServiceServerImpl) GetTaskDefinitionRevision(_ context.Context, configuration *definitions.Configuration) (*definitions.Revision, error) {
	var env *environment.ExecutionEnvironment
	err := json.Unmarshal(configuration.Configuration, env)
	if err != nil {
		log.WithError(err).Error("Unable to unmarshal environment configuration object")
		return nil, err
	}

	taskDefinition, err := service.CreateTaskDefinition(
		env.ContainerDefinitions(),
		env.Volume(),
		env.TaskDefinitionFamily,
		env.TaskRoleArn,
	)
	if err != nil {
		log.WithError(err).Error("Unable to create task definition")
		return nil, err
	}
	return &definitions.Revision{Value: *taskDefinition.Revision}, nil
}

func (ServiceServerImpl) GetTaskDefinitionRevisionByHash(_ context.Context, hash *definitions.Hash) (*definitions.Revision, error) {
	cache := cache.GetCache()
	if cache.Cache == nil {
		return nil, fmt.Errorf("cache is not initialized yet")
	}
	cache.RLock()
	revision, ok := cache.Cache[hash.Value]
	cache.RUnlock()
	if !ok {
		return nil, fmt.Errorf("there are no revision with such hash")
	}
	return &definitions.Revision{Value: revision}, nil
}

func main() {
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	flag.Parse()
	log.SetLevel(config.Conf.ParseLogLevel())

	defer func() {
		config.CloseConnections()
	}()

	awsSession, err := service.InitAws()
	if err != nil {
		utils.ExitWithError(err, "Failed to init aws session", log.NewEntry(log.StandardLogger()))
	}
	service.AwsSess = awsSession

	if err = config.InitDBConnection(config.Conf.DbConnectionString); err != nil {
		utils.ExitWithError(err, "Failed to init DB client", log.NewEntry(log.StandardLogger()))
	}

	if err = config.InitRedisClusterConnection(); err != nil {
		utils.ExitWithError(err, "Failed to init redis connection", log.NewEntry(log.StandardLogger()))
	}

	listener, err := net.Listen("tcp", listen)
	if err != nil {
		log.WithError(err).Fatalf("failed to listen tcp port %s", listen)
	}

	server := grpc.NewServer()
	definitions.RegisterServiceServer(server, &ServiceServerImpl{})

	go func() {
		if err := server.Serve(listener); err != nil {
			log.WithError(err).Fatal("Failed to start task-definitions server")
		}
	}()
	log.Info("Service started")
	<-quit

	log.Info("Shutdown task-definitions ...")
	server.GracefulStop()
	log.Info("task-definitions exited")
}
