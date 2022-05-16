#!/bin/bash

set -e

CACHE_REGISTRY=${1}
APP_NAME=${2}
TARGET_NAME=${3}
DOCKERFILE=${4:-"Dockerfile"}

if [ -z "$CACHE_REGISTRY" ] || [ -z "$APP_NAME" ] || [ -z "$TARGET_NAME" ]; then
    echo "Usage: build-cache-images.sh cache-registry appname target-name [dockerfile-name]"
    exit 1
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

# Check if the image already exists
set +e
docker pull "${CACHE_NAME}"
EXITCODE=$?
set -e

# Image doesn't exist, so we need to build it
if [ $EXITCODE -eq 0 ]; then
    echo "Found cached image"
else
    echo "Cached image not found; building it"
    docker build -f "${APP_DIR}/${DOCKERFILE}" "${APP_DIR}/." -t "${CACHE_NAME}"
    docker push "${CACHE_NAME}"
fi

# Tag the image with the desired tag
echo "Tagging image as ${TARGET_NAME}"
docker tag "${CACHE_NAME}" "${TARGET_NAME}"
