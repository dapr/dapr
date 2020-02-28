# ------------------------------------------------------------
# Copyright (c) Microsoft Corporation.
# Licensed under the MIT License.
# ------------------------------------------------------------


# Docker image build and push setting
DOCKER:=docker
DOCKERFILE_DIR?=./docker

ifeq ($(origin DEBUG), undefined)
  DOCKERFILE:=Dockerfile
else ifeq ($(DEBUG),0)
  DOCKERFILE:=Dockerfile
else
  DOCKERFILE:=Dockerfile-debug
endif

# Supported docker image architecture
DOCKERMUTI_ARCH=linux/amd64,linux/arm/v7

################################################################################
# Target: docker-build, docker-push                                            #
################################################################################

LINUX_BINS_OUT_DIR=$(OUT_DIR)/linux_$(GOARCH)
DOCKER_IMAGE_TAG=$(DAPR_REGISTRY)/$(RELEASE_NAME):$(DAPR_TAG)

ifeq ($(LATEST_RELEASE),true)
DOCKER_IMAGE_LATEST_TAG=$(DAPR_REGISTRY)/$(RELEASE_NAME):$(LATEST_TAG)
endif

# To use buildx: https://github.com/docker/buildx#docker-ce
export DOCKER_CLI_EXPERIMENTAL=enabled

# check the required environment variables
check-docker-env:
ifeq ($(DAPR_REGISTRY),)
	$(error DAPR_REGISTRY environment variable must be set)
endif
ifeq ($(DAPR_TAG),)
	$(error DAPR_TAG environment variable must be set)
endif

# build docker image for linux
docker-build: check-docker-env
	$(info Building $(DOCKER_IMAGE_TAG) docker image ...)
	$(DOCKER) build --build-arg TARGETPLATFORM=linux/amd64 -f $(DOCKERFILE_DIR)/$(DOCKERFILE) $(OUT_DIR)/. -t $(DOCKER_IMAGE_TAG)

# push docker image to the registry
docker-push: docker-build
	$(info Pushing $(DOCKER_IMAGE_TAG) docker image ...)
	$(DOCKER) push $(DOCKER_IMAGE_TAG)

# publish muti-arch docker image to the registry
docker-publish: check-docker-env
	-$(DOCKER) buildx create --use --name daprbuild
	-$(DOCKER) run --rm --privileged multiarch/qemu-user-static --reset -p yes
	$(info Pushing $(DOCKER_IMAGE_TAG) docker image ...)
	$(DOCKER) buildx build --platform $(DOCKERMUTI_ARCH) -t $(DOCKER_IMAGE_TAG) $(OUT_DIR) -f $(DOCKERFILE_DIR)/$(DOCKERFILE) --push
ifeq ($(LATEST_RELEASE),true)
	$(info Pushing $(DOCKER_IMAGE_LATEST_TAG) docker image ...)
	$(DOCKER) buildx build --platform $(DOCKERMUTI_ARCH) -t $(DOCKER_IMAGE_LATEST_TAG) $(OUT_DIR) -f $(DOCKERFILE_DIR)/$(DOCKERFILE) --push
endif

################################################################################
# Target: build-dev-container, push-dev-container                              #
################################################################################

# Update whenever you upgrade dev container image
DEV_CONTAINER_VERSION_TAG?=0.1.1

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
