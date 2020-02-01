# ------------------------------------------------------------
# Copyright (c) Microsoft Corporation.
# Licensed under the MIT License.
# ------------------------------------------------------------

# Update whenever you upgrade dev container image
DEV_CONTAINER_VERSION_TAG?=0.1.0

# Dapr container image name
DEV_CONTAINER_IMAGE_NAME=dapr-dev

DEV_CONTAINER_DOCKERFILE=Dockerfile-dev
DOCKERFILE_DIR=./docker

check-docker-env-for-dev-container:
ifeq ($(DAPR_REGISTRY),)
	$(error DAPR_REGISTRY environment variable must be set)
endif

build-dev-container: check-docker-env-for-dev-container
	$(DOCKER) build -f $(DOCKERFILE_DIR)/$(DEV_CONTAINER_DOCKERFILE) $(DOCKERFILE_DIR)/. -t $(DAPR_REGISTRY)/$(DEV_CONTAINER_IMAGE_NAME):$(DEV_CONTAINER_VERSION_TAG)

push-dev-container: check-docker-env-for-dev-container
	$(DOCKER) push $(DAPR_REGISTRY)/$(DEV_CONTAINER_IMAGE_NAME):$(DEV_CONTAINER_VERSION_TAG)
