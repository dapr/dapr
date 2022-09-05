#!/bin/sh

# Use this script to run the app manually

LOG_LEVEL=${1:-"debug"}
export APP_PORT=6009
#export APP_PROTOCOL=http
export APP_PROTOCOL=grpc

~/.dapr/bin/daprd \
    --app-id healthcheck \
    --dapr-http-port 3602 \
    --dapr-grpc-port 6602 \
    --metrics-port 9090 \
    --app-port $APP_PORT \
    --app-protocol $APP_PROTOCOL \
    --components-path ./components \
    --enable-app-health-check=true \
    --app-health-check-path=/healthz \
    --app-health-probe-interval=15 \
    --log-level "$LOG_LEVEL"
