#!/bin/bash

set -e

CACHE_REGISTRY=${1}
APP_NAME=${2}
TARGET_NAME=${3}
PUSH=${4:-"false"}
DOCKERFILE=${5:-"Dockerfile"}

if [ -z "$CACHE_REGISTRY" ] || [ -z "$APP_NAME" ] || [ -z "$TARGET_NAME" ]; then
    echo "Usage: build-cache-images.sh cache-registry appname target-name [dockerfile-name]"
    exit 1
fi

# Check if crane is installed
USE_CRANE="false"
if command -v crane &> /dev/null ; then
    USE_CRANE="true"
else
    echo "WARN: crane is not installed - copying images will be slower."
    echo -e "To install crane, run:\n  go install github.com/google/go-containerregistry/cmd/crane@latest"
fi

# cd to the directory of this script
cd "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")"

# Ensure the app exists
APP_DIR="apps/${APP_NAME}"
if [ ! -d "$APP_DIR" ]; then
    echo "App $APP_NAME does not exist"
    exit 1
fi
if [ ! -e "$APP_DIR/${DOCKERFILE}" ]; then
    echo "No Dockerfile (${DOCKERFILE}) found in $APP_NAME folder"
    exit 1
fi

function hashdir(){
    starting_dir="$(pwd)"
    target_dir="$1"
    cd "$target_dir"

    array=($(find . -not -type d))
    readarray -t sorted < <(sort < <(printf '%s\n' "${array[@]}"))
    result=""
    for i in ${sorted[@]};do
        result+="$(sha256sum $i)\n"
    done
    cd "$starting_dir"
    echo -e "$result" | sha256sum | awk '{ print $1 }'
}

# Compute the hash of the app's files
HASH=$(hashdir "$APP_DIR")
echo "HASH: ${HASH:0:10} (${HASH})"
HASH="${HASH:0:10}"
CACHE_NAME="${CACHE_REGISTRY}/${APP_DIR}:${DOCKERFILE}-${HASH}"

# Build and push the Docker image, invoked when the image isn't in the cache already
function buildAndPushCache(){
    echo "Cached image not found; building it"
    docker build -f "${APP_DIR}/${DOCKERFILE}" "${APP_DIR}/." -t "${CACHE_NAME}"

    # Push the image. This may fail if we're not authenticated, and it's fine
    echo "Pushing image ${CACHE_NAME}…"
    docker push "${CACHE_NAME}" \
        || echo "Push failed - continuing regardless"
}

function useCrane(){
    if [ "$PUSH" != "true" ] || [ "$USE_CRANE" != "true" ] ; then
        return 1
    fi

    # Copy the image directly to the target registry
    echo "Trying to copy image using crane to ${TARGET_NAME}"
    crane copy "${CACHE_NAME}" "${TARGET_NAME}"
}

function useDocker(){
    docker pull "${CACHE_NAME}" && echo "Found cached image" \
        || buildAndPushCache

    # Tag the image with the desired tag
    echo "Tagging image as ${TARGET_NAME}"
    docker tag "${CACHE_NAME}" "${TARGET_NAME}"

    if [ "$PUSH" == "true" ] ; then
        echo "Pushing image to ${TARGET_NAME}"
        docker push "${TARGET_NAME}"
    fi
}

# Check if the image already exists, otherwise build it
# Try copying it with crane first, then fallback to pulling with Docker
useCrane || useDocker
