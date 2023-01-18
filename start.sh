#!/bin/bash

docker-compose pull
docker-compose up -d --scale router=2
