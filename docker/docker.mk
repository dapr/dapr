# ------------------------------------------------------------
# Copyright (c) Microsoft Corporation.
# Licensed under the MIT License.
# ------------------------------------------------------------


# Docker image build and push setting
DOCKER:=docker
DOCKERFILE_DIR?=./docker

DAPR_SYSTEM_IMAGE_NAME=$(RELEASE_NAME)
DAPR_RUNTIME_IMAGE_NAME=daprd
DAPR_PLACEMENT_IMAGE_NAME=placement
DAPR_SENTRY_IMAGE_NAME=sentry

ifeq ($(TARGET_OS), windows)
  DOCKERFILE:=Dockerfile-windows
else ifeq ($(origin DEBUG), undefined)
  DOCKERFILE:=Dockerfile
else ifeq ($(DEBUG),0)
  DOCKERFILE:=Dockerfile
else
  DOCKERFILE:=Dockerfile-debug
endif

# Supported docker image architecture
DOCKERMUTI_ARCH=linux-amd64 linux-arm windows-amd64

################################################################################
# Target: docker-build, docker-push                                            #
################################################################################

LINUX_BINS_OUT_DIR=$(OUT_DIR)/linux_$(GOARCH)
DOCKER_IMAGE_TAG=$(DAPR_REGISTRY)/$(DAPR_SYSTEM_IMAGE_NAME):$(DAPR_TAG)
DAPR_RUNTIME_DOCKER_IMAGE_TAG=$(DAPR_REGISTRY)/$(DAPR_RUNTIME_IMAGE_NAME):$(DAPR_TAG)
DAPR_PLACEMENT_DOCKER_IMAGE_TAG=$(DAPR_REGISTRY)/$(DAPR_PLACEMENT_IMAGE_NAME):$(DAPR_TAG)
DAPR_SENTRY_DOCKER_IMAGE_TAG=$(DAPR_REGISTRY)/$(DAPR_SENTRY_IMAGE_NAME):$(DAPR_TAG)

ifeq ($(LATEST_RELEASE),true)
DOCKER_IMAGE_LATEST_TAG=$(DAPR_REGISTRY)/$(DAPR_SYSTEM_IMAGE_NAME):$(LATEST_TAG)
DAPR_RUNTIME_DOCKER_IMAGE_LATEST_TAG=$(DAPR_REGISTRY)/$(DAPR_RUNTIME_IMAGE_NAME):$(LATEST_TAG)
DAPR_PLACEMENT_DOCKER_IMAGE_LATEST_TAG=$(DAPR_REGISTRY)/$(DAPR_PLACEMENT_IMAGE_NAME):$(LATEST_TAG)
DAPR_SENTRY_DOCKER_IMAGE_LATEST_TAG=$(DAPR_REGISTRY)/$(DAPR_SENTRY_IMAGE_NAME):$(LATEST_TAG)
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

check-arch:
ifeq ($(TARGET_OS),)
	$(error TARGET_OS environment variable must be set)
endif
ifeq ($(TARGET_ARCH),)
	$(error TARGET_ARCH environment variable must be set)
endif

# build docker image for linux
BIN_PATH=$(OUT_DIR)/$(TARGET_OS)_$(TARGET_ARCH)/release

docker-build: check-docker-env check-arch
	$(info Building $(DOCKER_IMAGE_TAG) docker image ...)
	$(DOCKER) build --build-arg PKG_FILES=* -f $(DOCKERFILE_DIR)/$(DOCKERFILE) $(BIN_PATH) -t $(DOCKER_IMAGE_TAG)-$(TARGET_OS)-$(TARGET_ARCH)
	$(DOCKER) build --build-arg PKG_FILES=daprd -f $(DOCKERFILE_DIR)/$(DOCKERFILE) $(BIN_PATH) -t $(DAPR_RUNTIME_DOCKER_IMAGE_TAG)-$(TARGET_OS)-$(TARGET_ARCH)
	$(DOCKER) build --build-arg PKG_FILES=placement -f $(DOCKERFILE_DIR)/$(DOCKERFILE) $(BIN_PATH) -t $(DAPR_PLACEMENT_DOCKER_IMAGE_TAG)-$(TARGET_OS)-$(TARGET_ARCH)
	$(DOCKER) build --build-arg PKG_FILES=sentry -f $(DOCKERFILE_DIR)/$(DOCKERFILE) $(BIN_PATH) -t $(DAPR_SENTRY_DOCKER_IMAGE_TAG)-$(TARGET_OS)-$(TARGET_ARCH)

# push docker image to the registry
docker-push: docker-build
	$(info Pushing $(DOCKER_IMAGE_TAG) docker image ...)
	$(DOCKER) push $(DOCKER_IMAGE_TAG)-$(TARGET_OS)-$(TARGET_ARCH)
	$(DOCKER) push $(DAPR_RUNTIME_DOCKER_IMAGE_TAG)-$(TARGET_OS)-$(TARGET_ARCH)
	$(DOCKER) push $(DAPR_PLACEMENT_DOCKER_IMAGE_TAG)-$(TARGET_OS)-$(TARGET_ARCH)
	$(DOCKER) push $(DAPR_SENTRY_DOCKER_IMAGE_TAG)-$(TARGET_OS)-$(TARGET_ARCH)

# publish muti-arch docker image to the registry
docker-manifest-create: check-docker-env
	$(DOCKER) manifest create $(DOCKER_IMAGE_TAG) $(DOCKERMUTI_ARCH:%=$(DOCKER_IMAGE_TAG)-%)
	$(DOCKER) manifest create $(DAPR_RUNTIME_DOCKER_IMAGE_TAG) $(DOCKERMUTI_ARCH:%=$(DAPR_RUNTIME_DOCKER_IMAGE_TAG)-%)
	$(DOCKER) manifest create $(DAPR_PLACEMENT_DOCKER_IMAGE_TAG) $(DOCKERMUTI_ARCH:%=$(DAPR_PLACEMENT_DOCKER_IMAGE_TAG)-%)
	$(DOCKER) manifest create $(DAPR_SENTRY_DOCKER_IMAGE_TAG) $(DOCKERMUTI_ARCH:%=$(DAPR_SENTRY_DOCKER_IMAGE_TAG)-%)
ifeq ($(LATEST_RELEASE),true)
	$(DOCKER) manifest create $(DOCKER_IMAGE_LATEST_TAG) $(DOCKERMUTI_ARCH:%=$(DOCKER_IMAGE_TAG)-%)
	$(DOCKER) manifest create $(DAPR_RUNTIME_DOCKER_IMAGE_LATEST_TAG) $(DOCKERMUTI_ARCH:%=$(DAPR_RUNTIME_DOCKER_IMAGE_TAG)-%)
	$(DOCKER) manifest create $(DAPR_PLACEMENT_DOCKER_IMAGE_LATEST_TAG) $(DOCKERMUTI_ARCH:%=$(DAPR_PLACEMENT_DOCKER_IMAGE_TAG)-%)
	$(DOCKER) manifest create $(DAPR_SENTRY_DOCKER_IMAGE_LATEST_TAG) $(DOCKERMUTI_ARCH:%=$(DAPR_SENTRY_DOCKER_IMAGE_TAG)-%)
endif

docker-publish: docker-manifest-create
	$(DOCKER) manifest push $(DOCKER_IMAGE_TAG)
	$(DOCKER) manifest push $(DAPR_RUNTIME_DOCKER_IMAGE_TAG)
	$(DOCKER) manifest push $(DAPR_PLACEMENT_DOCKER_IMAGE_TAG)
	$(DOCKER) manifest push $(DAPR_SENTRY_DOCKER_IMAGE_TAG)
ifeq ($(LATEST_RELEASE),true)
	$(DOCKER) manifest push $(DOCKER_IMAGE_LATEST_TAG)
	$(DOCKER) manifest push $(DAPR_RUNTIME_DOCKER_IMAGE_LATEST_TAG)
	$(DOCKER) manifest push $(DAPR_PLACEMENT_DOCKER_IMAGE_LATEST_TAG)
	$(DOCKER) manifest push $(DAPR_SENTRY_DOCKER_IMAGE_LATEST_TAG)
endif

################################################################################
# Target: build-dev-container, push-dev-container                              #
################################################################################

# Update whenever you upgrade dev container image
DEV_CONTAINER_VERSION_TAG?=0.1.2

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
