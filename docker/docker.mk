# ------------------------------------------------------------
# Copyright (c) Microsoft Corporation and Dapr Contributors.
# Licensed under the MIT License.
# ------------------------------------------------------------


# Docker image build and push setting
DOCKER:=docker
DOCKERFILE_DIR?=./docker

DAPR_SYSTEM_IMAGE_NAME=$(RELEASE_NAME)
DAPR_RUNTIME_IMAGE_NAME=daprd
DAPR_PLACEMENT_IMAGE_NAME=placement
DAPR_SENTRY_IMAGE_NAME=sentry

# build docker image for linux
BIN_PATH=$(OUT_DIR)/$(TARGET_OS)_$(TARGET_ARCH)

ifeq ($(TARGET_OS), windows)
  DOCKERFILE:=Dockerfile-windows
  BIN_PATH := $(BIN_PATH)/release
else ifeq ($(origin DEBUG), undefined)
  DOCKERFILE:=Dockerfile
  BIN_PATH := $(BIN_PATH)/release
else ifeq ($(DEBUG),0)
  DOCKERFILE:=Dockerfile
  BIN_PATH := $(BIN_PATH)/release
else
  DOCKERFILE:=Dockerfile-debug
  BIN_PATH := $(BIN_PATH)/debug
endif

ifeq ($(TARGET_ARCH),arm)
  DOCKER_IMAGE_PLATFORM:=$(TARGET_OS)/arm/v7
else ifeq ($(TARGET_ARCH),arm64)
  DOCKER_IMAGE_PLATFORM:=$(TARGET_OS)/arm64/v8
else
  DOCKER_IMAGE_PLATFORM:=$(TARGET_OS)/amd64
endif

# Supported docker image architecture
DOCKERMUTI_ARCH=linux-amd64 linux-arm linux-arm64 windows-amd64

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


docker-build: check-docker-env check-arch
	$(info Building $(DOCKER_IMAGE_TAG) docker image ...)
ifeq ($(TARGET_ARCH),amd64)
	$(DOCKER) build --build-arg PKG_FILES=* -f $(DOCKERFILE_DIR)/$(DOCKERFILE) $(BIN_PATH) -t $(DOCKER_IMAGE_TAG)-$(TARGET_OS)-$(TARGET_ARCH)
	$(DOCKER) build --build-arg PKG_FILES=daprd -f $(DOCKERFILE_DIR)/$(DOCKERFILE) $(BIN_PATH) -t $(DAPR_RUNTIME_DOCKER_IMAGE_TAG)-$(TARGET_OS)-$(TARGET_ARCH)
	$(DOCKER) build --build-arg PKG_FILES=placement -f $(DOCKERFILE_DIR)/$(DOCKERFILE) $(BIN_PATH) -t $(DAPR_PLACEMENT_DOCKER_IMAGE_TAG)-$(TARGET_OS)-$(TARGET_ARCH)
	$(DOCKER) build --build-arg PKG_FILES=sentry -f $(DOCKERFILE_DIR)/$(DOCKERFILE) $(BIN_PATH) -t $(DAPR_SENTRY_DOCKER_IMAGE_TAG)-$(TARGET_OS)-$(TARGET_ARCH)
else
	-$(DOCKER) buildx create --use --name daprbuild
	-$(DOCKER) run --rm --privileged multiarch/qemu-user-static --reset -p yes
	$(DOCKER) buildx build --build-arg PKG_FILES=* --platform $(DOCKER_IMAGE_PLATFORM) -f $(DOCKERFILE_DIR)/$(DOCKERFILE) $(BIN_PATH) -t $(DOCKER_IMAGE_TAG)-$(TARGET_OS)-$(TARGET_ARCH)
	$(DOCKER) buildx build --build-arg PKG_FILES=daprd --platform $(DOCKER_IMAGE_PLATFORM) -f $(DOCKERFILE_DIR)/$(DOCKERFILE) $(BIN_PATH) -t $(DAPR_RUNTIME_DOCKER_IMAGE_TAG)-$(TARGET_OS)-$(TARGET_ARCH)
	$(DOCKER) buildx build --build-arg PKG_FILES=placement --platform $(DOCKER_IMAGE_PLATFORM) -f $(DOCKERFILE_DIR)/$(DOCKERFILE) $(BIN_PATH) -t $(DAPR_PLACEMENT_DOCKER_IMAGE_TAG)-$(TARGET_OS)-$(TARGET_ARCH)
	$(DOCKER) buildx build --build-arg PKG_FILES=sentry --platform $(DOCKER_IMAGE_PLATFORM) -f $(DOCKERFILE_DIR)/$(DOCKERFILE) $(BIN_PATH) -t $(DAPR_SENTRY_DOCKER_IMAGE_TAG)-$(TARGET_OS)-$(TARGET_ARCH)
endif

# push docker image to the registry
docker-push: docker-build
	$(info Pushing $(DOCKER_IMAGE_TAG) docker image ...)
ifeq ($(TARGET_ARCH),amd64)
	$(DOCKER) push $(DOCKER_IMAGE_TAG)-$(TARGET_OS)-$(TARGET_ARCH)
	$(DOCKER) push $(DAPR_RUNTIME_DOCKER_IMAGE_TAG)-$(TARGET_OS)-$(TARGET_ARCH)
	$(DOCKER) push $(DAPR_PLACEMENT_DOCKER_IMAGE_TAG)-$(TARGET_OS)-$(TARGET_ARCH)
	$(DOCKER) push $(DAPR_SENTRY_DOCKER_IMAGE_TAG)-$(TARGET_OS)-$(TARGET_ARCH)
else
	-$(DOCKER) buildx create --use --name daprbuild
	-$(DOCKER) run --rm --privileged multiarch/qemu-user-static --reset -p yes
	$(DOCKER) buildx build --build-arg PKG_FILES=* --platform $(DOCKER_IMAGE_PLATFORM) -f $(DOCKERFILE_DIR)/$(DOCKERFILE) $(BIN_PATH) -t $(DOCKER_IMAGE_TAG)-$(TARGET_OS)-$(TARGET_ARCH) --push
	$(DOCKER) buildx build --build-arg PKG_FILES=daprd --platform $(DOCKER_IMAGE_PLATFORM) -f $(DOCKERFILE_DIR)/$(DOCKERFILE) $(BIN_PATH) -t $(DAPR_RUNTIME_DOCKER_IMAGE_TAG)-$(TARGET_OS)-$(TARGET_ARCH) --push
	$(DOCKER) buildx build --build-arg PKG_FILES=placement --platform $(DOCKER_IMAGE_PLATFORM) -f $(DOCKERFILE_DIR)/$(DOCKERFILE) $(BIN_PATH) -t $(DAPR_PLACEMENT_DOCKER_IMAGE_TAG)-$(TARGET_OS)-$(TARGET_ARCH) --push
	$(DOCKER) buildx build --build-arg PKG_FILES=sentry --platform $(DOCKER_IMAGE_PLATFORM) -f $(DOCKERFILE_DIR)/$(DOCKERFILE) $(BIN_PATH) -t $(DAPR_SENTRY_DOCKER_IMAGE_TAG)-$(TARGET_OS)-$(TARGET_ARCH) --push
endif

# push docker image to kind cluster
docker-push-kind: docker-build
	$(info Pushing $(DOCKER_IMAGE_TAG) docker image to kind cluster...)
	kind load docker-image $(DOCKER_IMAGE_TAG)-$(TARGET_OS)-$(TARGET_ARCH)
	kind load docker-image $(DAPR_RUNTIME_DOCKER_IMAGE_TAG)-$(TARGET_OS)-$(TARGET_ARCH)
	kind load docker-image $(DAPR_PLACEMENT_DOCKER_IMAGE_TAG)-$(TARGET_OS)-$(TARGET_ARCH)
	kind load docker-image $(DAPR_SENTRY_DOCKER_IMAGE_TAG)-$(TARGET_OS)-$(TARGET_ARCH)

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

check-windows-version:
ifeq ($(WINDOWS_VERSION),)
	$(error WINDOWS_VERSION environment variable must be set)
endif

docker-windows-base-build: check-windows-version
	$(DOCKER) build --build-arg WINDOWS_VERSION=$(WINDOWS_VERSION) -f $(DOCKERFILE_DIR)/$(DOCKERFILE)-base . -t $(DAPR_REGISTRY)/windows-base:$(WINDOWS_VERSION)
	$(DOCKER) build --build-arg WINDOWS_VERSION=$(WINDOWS_VERSION) -f $(DOCKERFILE_DIR)/$(DOCKERFILE)-java-base . -t $(DAPR_REGISTRY)/windows-java-base:$(WINDOWS_VERSION)
	$(DOCKER) build --build-arg WINDOWS_VERSION=$(WINDOWS_VERSION) -f $(DOCKERFILE_DIR)/$(DOCKERFILE)-php-base . -t $(DAPR_REGISTRY)/windows-php-base:$(WINDOWS_VERSION)
	$(DOCKER) build --build-arg WINDOWS_VERSION=$(WINDOWS_VERSION) -f $(DOCKERFILE_DIR)/$(DOCKERFILE)-python-base . -t $(DAPR_REGISTRY)/windows-python-base:$(WINDOWS_VERSION)

docker-windows-base-push: check-windows-version
	$(DOCKER) push $(DAPR_REGISTRY)/windows-base:$(WINDOWS_VERSION)
	$(DOCKER) push $(DAPR_REGISTRY)/windows-java-base:$(WINDOWS_VERSION)
	$(DOCKER) push $(DAPR_REGISTRY)/windows-php-base:$(WINDOWS_VERSION)
	$(DOCKER) push $(DAPR_REGISTRY)/windows-python-base:$(WINDOWS_VERSION)

################################################################################
# Target: build-dev-container, push-dev-container                              #
################################################################################

# Update whenever you upgrade dev container image
DEV_CONTAINER_VERSION_TAG?=0.1.5

# Use this to pin a specific version of the Dapr CLI to a devcontainer
DEV_CONTAINER_CLI_TAG?=1.5.0

# Dapr container image name
DEV_CONTAINER_IMAGE_NAME=dapr-dev

DEV_CONTAINER_DOCKERFILE=Dockerfile-dev
DOCKERFILE_DIR=./docker

check-docker-env-for-dev-container:
ifeq ($(DAPR_REGISTRY),)
	$(error DAPR_REGISTRY environment variable must be set)
endif

build-dev-container:
ifeq ($(DAPR_REGISTRY),)
	$(info DAPR_REGISTRY environment variable not set, tagging image without registry prefix.)
	$(info `make tag-dev-container` should be run with DAPR_REGISTRY before `make push-dev-container.)
	$(DOCKER) build --build-arg DAPR_CLI_VERSION=$(DEV_CONTAINER_CLI_TAG) -f $(DOCKERFILE_DIR)/$(DEV_CONTAINER_DOCKERFILE) $(DOCKERFILE_DIR)/. -t $(DEV_CONTAINER_IMAGE_NAME):$(DEV_CONTAINER_VERSION_TAG)
else
	$(DOCKER) build --build-arg DAPR_CLI_VERSION=$(DEV_CONTAINER_CLI_TAG) -f $(DOCKERFILE_DIR)/$(DEV_CONTAINER_DOCKERFILE) $(DOCKERFILE_DIR)/. -t $(DAPR_REGISTRY)/$(DEV_CONTAINER_IMAGE_NAME):$(DEV_CONTAINER_VERSION_TAG)
endif

tag-dev-container: check-docker-env-for-dev-container
	$(DOCKER) tag $(DEV_CONTAINER_IMAGE_NAME):$(DEV_CONTAINER_VERSION_TAG) $(DAPR_REGISTRY)/$(DEV_CONTAINER_IMAGE_NAME):$(DEV_CONTAINER_VERSION_TAG)

push-dev-container: check-docker-env-for-dev-container
	$(DOCKER) push $(DAPR_REGISTRY)/$(DEV_CONTAINER_IMAGE_NAME):$(DEV_CONTAINER_VERSION_TAG)
