#!/bin/bash

BASEDIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "${BASEDIR}" || exit

  build() {
    case "$1" in
      "")
        docker compose -f "$BASEDIR/build/docker-compose.yaml" build
        ;;

      "service")
        service_name=$2
        docker compose -f "$BASEDIR/build/docker-compose.yaml" build "$service_name"
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
          --help | -h               Print help
      Arguments:
      	  build  [service]  <name>  Build all/selected images
      	  "
      echo_telegram
      exit 0
  }


case "$1" in
    build)
      build "$2" "$3"
      ;;
    *)
      echo_help
      exit 1
      ;;
esac
