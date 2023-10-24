#!/bin/bash

BASEDIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "${BASEDIR}" || exit


  start() {
    networkName="e3s-network"
    networkDescription=$(docker network ls -f name=$networkName | grep $networkName)
    if [ -z "$networkDescription" ]; then
      # create network with name $networkName
      docker network create -d bridge "$networkName"
    fi

    # start postgres and redis
    docker compose -f "$BASEDIR/data-layer/docker-compose.yaml" up -d
    # start other services
    docker compose -f "$BASEDIR/docker-compose.yaml" up -d
  }

  down() {
    # stop services
    docker compose -f "$BASEDIR/docker-compose.yaml" down -t 540
    # stop postgres and redis
    docker compose -f "$BASEDIR/data-layer/docker-compose.yaml" down

    networkName="e3s-network"
    networkDescription=$(docker network ls -f name=$networkName | grep $networkName)
    if [ ! -z "$networkDescription" ]; then
      # delete network with name $networkName
      docker network rm $networkName
    fi
  }

  shutdown() {
    echo
    # stop services
    docker compose -f "$BASEDIR/docker-compose.yaml" down -v -t 540
    # stop postgres and redis
    docker compose -f "$BASEDIR/data-layer/docker-compose.yaml" down -v

    networkName="e3s-network"
    networkDescription=$(docker network ls -f name=$networkName | grep $networkName)
    if [ ! -z "$networkDescription" ]; then
      # delete network with name $networkName
      docker network rm $networkName
    fi
  }

  build() {
    docker compose -f "$BASEDIR/build/docker-compose.yaml" build
  }

  service_start() {
    service_name=$1
    if [ -z "$service_name" ]; then
      read -p "Service name is not passed. Do you want to start all services? (y/n) [y]: "
      if [[ $REPLY =~ ^[Yy]*$ ]]; then
        docker compose -f "$BASEDIR/docker-compose.yaml" up -d
      else
        exit 1
      fi
    else
      docker compose -f "$BASEDIR/docker-compose.yaml" up -d --no-deps $service_name
      ret=$?
      if [ $ret -ne 0 ]; then
        echo "failed to start service $service_name"
        exit 1
      fi
    fi
  }

  service_stop() {
    service_name=$1
     if [ -z "$service_name" ]; then
      read -p "Service name is not passed. Do you want to stop all services? (y/n) [y]: "
      if [[ $REPLY =~ ^[Yy]*$ ]]; then
        docker compose -f "$BASEDIR/docker-compose.yaml" down -t 540
      else
        exit 1
      fi
    else
      docker compose -f "$BASEDIR/docker-compose.yaml" stop $service_name -t 540
      ret=$?
      if [ $ret -ne 0 ]; then
        echo "failed to stop service $service_name"
        exit 1
      fi
    fi
  }

  version() {
    echo
  }

  echo_warning() {
    echo "
      WARNING! $1"
  }

  echo_telegram() {
    echo "
      For more help join telegram channel: https://t.me/zebrunner
      "
  }

  echo_help() {
    echo "
      Usage: ./zebrunner.sh [option]
      Flags:
          --help | -h    Print help
      Arguments:
      	  start                             Start containers for all layers
      	  down                              Stop and remove containers for all layers
      	  restart                           Restart containers for all layers
      	  shutdown                          Stop and remove containers for all layers, clear volumes
      	  build                             Build images
      	  service_start <service_name>      Start one all service layer containers
      	  service_stop  <service_name>      Stop one or all service layer containers
      	  service_restart <service_name>    Restart one or all service layer containers
      	  "
      echo_telegram
      exit 0
  }


case "$1" in
    setup)
        setup
        ;;
    start)
	      start
        ;;
    restart)
        down
        start
        ;;
    down)
        down
        ;;
    shutdown)
        shutdown
        ;;
    build)
        build
        ;;
    service_start)
        service_start "$2"
        ;;
    service_stop)
        service_stop "$2"
        ;;
    service_restart)
        service_stop "$2"
        service_start "$2"
        ;;
    version)
        version
        ;;
    --help | -h)
        echo_help
        ;;
    *)
        echo_help
        exit 1
        ;;
esac
