#!/bin/sh

# Use this script to run the app manually

export APP_PORT=6009
#export APP_PROTOCOL=http
export APP_PROTOCOL=grpc

go run .
