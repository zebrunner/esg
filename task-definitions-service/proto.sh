#!/bin/bash

export GO_PATH=~/go
export PATH="$PATH:$(go env GOPATH)/bin"

protoc --go_out=./definitions --go_opt=paths=source_relative \
    --go-grpc_out=./definitions --go-grpc_opt=paths=source_relative \
    definitions.proto
