#!/bin/bash

BASEDIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "${BASEDIR}" || exit

graceful_timeout=600
networkName="e3s-network"


  start() {
    # Create network if not exist
    networkDescription=$(docker network ls -f name=$networkName | grep $networkName)
    if [ -z "$networkDescription" ]; then
      # Create network with name $networkName
      docker network create -d bridge "$networkName" > /dev/null
      echo "Network $networkName Created"
    fi

    case "$1" in
      "")
        # start postgres and redis
        docker compose -f "$BASEDIR/data-layer/docker-compose.yaml" up -d
        # start other services
        docker compose -f "$BASEDIR/docker-compose.yaml" up -d
        ;;

      data)
        data_name=$2
        if [ -z "$data_name" ]; then
            docker compose -f "$BASEDIR/data-layer/docker-compose.yaml" up -d
        else
          docker compose -f "$BASEDIR/data-layer/docker-compose.yaml" up -d --no-deps "$data_name"
          ret=$?
          if [ $ret -ne 0 ]; then
            echo "Failed to start data $data_name"
            exit 1
          fi
        fi
        ;;

      service)
        service_name=$2
        if [ -z "$service_name" ]; then
            docker compose -f "$BASEDIR/docker-compose.yaml" up -d
        else
          docker compose -f "$BASEDIR/docker-compose.yaml" up -d --no-deps "$service_name"
          ret=$?
          if [ $ret -ne 0 ]; then
            echo "Failed to start service $service_name"
            exit 1
          fi
        fi
        ;;

      *)
        echo_warning "Wrong input"
        exit 1
        ;;
    esac
  }

  stop() {
    case "$1" in
      "")
        # stop services
        docker compose -f "$BASEDIR/docker-compose.yaml" stop -t $graceful_timeout
        # stop postgres and redis
        docker compose -f "$BASEDIR/data-layer/docker-compose.yaml" stop
        ;;

      data)
        data_name=$2
        if [ -z "$data_name" ]; then
            docker compose -f "$BASEDIR/data-layer/docker-compose.yaml" stop
        else
          docker compose -f "$BASEDIR/data-layer/docker-compose.yaml" stop "$data_name"
          ret=$?
          if [ $ret -ne 0 ]; then
            echo "Failed to stop data $data_name"
            exit 1
          fi
        fi
        ;;

      service)
        service_name=$2
        if [ -z "$service_name" ]; then
            docker compose -f "$BASEDIR/docker-compose.yaml" stop -t $graceful_timeout
        else
          docker compose -f "$BASEDIR/docker-compose.yaml" stop -t $graceful_timeout "$service_name"
          ret=$?
          if [ $ret -ne 0 ]; then
            echo "Failed to stop service $service_name"
            exit 1
          fi
        fi
        ;;

      *)
        echo_warning "Wrong input"
        exit 1
        ;;
    esac
  }

  down() {
    case "$1" in
      "")
        # down services
        docker compose -f "$BASEDIR/docker-compose.yaml" down -t $graceful_timeout
        # down postgres and redis
        docker compose -f "$BASEDIR/data-layer/docker-compose.yaml" down
        ;;

      data)
        data_name=$2
        if [ -z "$data_name" ]; then
            docker compose -f "$BASEDIR/data-layer/docker-compose.yaml" down
        else
          docker compose -f "$BASEDIR/data-layer/docker-compose.yaml" down "$data_name"
          ret=$?
          if [ $ret -ne 0 ]; then
            echo "Failed to down data $data_name"
            exit 1
          fi
        fi
        ;;

      service)
        service_name=$2
        if [ -z "$service_name" ]; then
            docker compose -f "$BASEDIR/docker-compose.yaml" down -t $graceful_timeout
        else
          docker compose -f "$BASEDIR/docker-compose.yaml" down -t $graceful_timeout "$service_name"
          ret=$?
          if [ $ret -ne 0 ]; then
            echo "Failed to down service $service_name"
            exit 1
          fi
        fi
        ;;

      *)
        echo_warning "Wrong input"
        exit 1
        ;;
    esac
  }

  shutdown() {
    case "$1" in
      "")
        echo_warning "Shutdown will erase all settings and data for \"${BASEDIR}\"!"
        read -r -p "Do you want to continue? (y/n) [y]: "
        if [[ $REPLY =~ ^[Yy]*$ ]]; then
          # shutdown services
          docker compose -f "$BASEDIR/docker-compose.yaml" down -v
          # shutdown postgres and redis
          docker compose -f "$BASEDIR/data-layer/docker-compose.yaml" down -v

          networkDescription=$(docker network ls -f name=$networkName | grep $networkName)
          if [ ! -z "$networkDescription" ]; then
            # delete network with name $networkName
            docker network rm $networkName
          fi
        fi
        ;;

      data)
        data_name=$2
        if [ -z "$data_name" ]; then
            read -r -p "The entire data layer and its volumes will be deleted. Do you want to continue? (y/n) [y]: "
            if [[ $REPLY =~ ^[Yy]*$ ]]; then
              docker compose -f "$BASEDIR/data-layer/docker-compose.yaml" down -v
            fi
        else
          read -r -p "$2 and its volumes will be deleted. Do you want to continue? (y/n) [y]: "
            if [[ $REPLY =~ ^[Yy]*$ ]]; then
              docker compose -f "$BASEDIR/data-layer/docker-compose.yaml" down -v "$data_name"
              ret=$?
              if [ $ret -ne 0 ]; then
                echo "Failed to shutdown data $data_name"
                exit 1
              fi
            fi
        fi
        ;;

      service)
        service_name=$2
        if [ -z "$service_name" ]; then
            read -r -p "The entire service layer and its volumes will be deleted. Do you want to continue? (y/n) [y]: "
            if [[ $REPLY =~ ^[Yy]*$ ]]; then
              docker compose -f "$BASEDIR/docker-compose.yaml" down -v -t $graceful_timeout
            fi
        else
          read -r -p "$2 and its volumes will be deleted. Do you want to continue? (y/n) [y]: "
            if [[ $REPLY =~ ^[Yy]*$ ]]; then
              docker compose -f "$BASEDIR/docker-compose.yaml" down -v -t $graceful_timeout "$service_name"
              ret=$?
              if [ $ret -ne 0 ]; then
                echo "Failed to shutdown data $data_name"
                exit 1
              fi
            fi
        fi
        ;;
      *)
        echo_warning "Wrong input"
        exit 1
        ;;
    esac
  }

  build() {
    docker compose -f "$BASEDIR/build/docker-compose.yaml" build
  }

  status() {
    watch -n 2 "docker ps --format '{{.Names}}   \t{{.Status}}'"
  }

  tasks() {
    case "$1" in
      list)
        ./scripts/list-tasks.sh
        ;;
      stop)
        ./scripts/stop-tasks.sh
        ;;
      *)
        echo_warning "Wrong input"
        exit 1
        ;;
    esac
  }

  describe() {
    case "$1" in
      cluster)
        ./scripts/describe-cluster.sh
        ;;
      instance)
        ./scripts/describe-instances.sh
        ;;
      task)
        ./scripts/describe-tasks.sh
        ;;
      *)
        echo_warning "Wrong input"
        exit 1
        ;;
    esac
  }

  instances() {
    case "$1" in
      list)
        ./scripts/list-instances.sh
        ;;
      *)
        echo_warning "Wrong input"
        exit 1
        ;;
    esac
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
          --help | -h                       Print help
      Arguments:
      	  start     [data|service] <name>         Start containers for selected layers
      	  stop      [data|service] <name>         Stop containers for selected layers
      	  down      [data|service] <name>         Stop and remove containers for selected layers
      	  shutdown  [data|service] <name>         Stop, remove containers, clear volumes for selected layers
      	  restart   [data|service] <name>         Down and start containers for selected layers
      	  build                                   Build images
      	  status                                  Show all containers statuses
          tasks     [list|stop]                   List all tasks or stop them
      	  describe  [cluster|instance|task]       Describe selected items
          instances [list]                        All cluster's container-instances list
      	  "
      echo_telegram
      exit 0
  }


case "$1" in
    start)
      start "$2" "$3"
      ;;
    stop)
      stop "$2" "$3"
      ;;
    down)
      down "$2" "$3"
      ;;
    shutdown)
      shutdown "$2" "$3"
      ;;
    restart)
      down "$2" "$3"
      start "$2" "$3"
      ;;
    build)
      build
      ;;
    status)
      status
      ;;
    tasks)
      tasks "$2"
      ;;
    describe)
      describe "$2"
      ;;
    instances)
      instances "$2"
      ;;
    --help | -h)
      echo_help
      ;;
    *)
      echo_help
      exit 1
      ;;
esac
