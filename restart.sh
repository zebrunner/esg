#!/bin/bash


docker-compose down
docker-compose up -d --scale router=2
