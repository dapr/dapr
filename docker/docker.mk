#
# Copyright 2021 The Dapr Authors
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#     http://www.apache.org/licenses/LICENSE-2.0
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.
#


# Docker image build and push setting
DOCKER:=docker
DOCKERFILE_DIR?=./docker

# If set to true, only the `dapr` image will be built and pushed to the registry.
# This is a "kitchen sink" image that contains all the components.
# The Helm charts will also be configured to use this image.
# This is useful for faster development and testing experience.
# If set to false, individual images for daprd, operator, sentry, injector, 
# and placement will be built and pushed to the registry.
ONLY_DAPR_IMAGE?=false

DAPR_SYSTEM_IMAGE_NAME?=$(RELEASE_NAME)
DAPR_RUNTIME_IMAGE_NAME?=daprd
DAPR_PLACEMENT_IMAGE_NAME?=placement
DAPR_SENTRY_IMAGE_NAME?=sentry
DAPR_OPERATOR_IMAGE_NAME?=operator
DAPR_INJECTOR_IMAGE_NAME?=injector

# build docker image for linux
BIN_PATH=$(OUT_DIR)/$(TARGET_OS)_$(TARGET_ARCH)

ifeq ($(TARGET_OS), windows)
  DOCKERFILE?=Dockerfile-windows
  BIN_PATH := $(BIN_PATH)/release
else ifeq ($(origin DEBUG), undefined)
  DOCKERFILE?=Dockerfile
  BIN_PATH := $(BIN_PATH)/release
else ifeq ($(DEBUG),0)
  DOCKERFILE?=Dockerfile
  BIN_PATH := $(BIN_PATH)/release
else
  DOCKERFILE?=Dockerfile-debug
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
DOCKER_MULTI_ARCH?=linux-amd64 linux-arm linux-arm64 windows-1809-amd64 windows-ltsc2022-amd64

################################################################################
# Target: docker-build, docker-push                                            #
################################################################################

# If WINDOWS_VERSION is set, use it as the Windows version in the docker image tag.
# Example, foo.io/dapr/dapr:1.10.0-rc.2-windows-ltsc2022-amd64 where ltsc2022 is the Windows version.
# If unset, use a simpler tag, example, foo.io/dapr/dapr:1.10.0-rc.2-windows-amd64.
ifneq ($(WINDOWS_VERSION),)
BUILD_ARGS=--build-arg WINDOWS_VERSION=$(WINDOWS_VERSION)
DOCKER_IMAGE_VARIANT=$(TARGET_OS)-$(WINDOWS_VERSION)-$(TARGET_ARCH)
else
DOCKER_IMAGE_VARIANT=$(TARGET_OS)-$(TARGET_ARCH)
endif

ifeq ($(MANIFEST_TAG),)
	MANIFEST_TAG=$(DAPR_TAG)
endif
ifeq ($(MANIFEST_LATEST_TAG),)
    # artursouza: this is intentional - latest manifest tag will point to immutable version tags.
    # For example: latest -> 1.11.0-linux-amd64 1.11.0-linux-arm 1.11.0-linux-arm64 ...
	MANIFEST_LATEST_TAG=$(DAPR_TAG)
endif

LINUX_BINS_OUT_DIR=$(OUT_DIR)/linux_$(GOARCH)
DOCKER_IMAGE=$(DAPR_REGISTRY)/$(DAPR_SYSTEM_IMAGE_NAME)
DAPR_RUNTIME_DOCKER_IMAGE=$(DAPR_REGISTRY)/$(DAPR_RUNTIME_IMAGE_NAME)
DAPR_PLACEMENT_DOCKER_IMAGE=$(DAPR_REGISTRY)/$(DAPR_PLACEMENT_IMAGE_NAME)
DAPR_SENTRY_DOCKER_IMAGE=$(DAPR_REGISTRY)/$(DAPR_SENTRY_IMAGE_NAME)
DAPR_OPERATOR_DOCKER_IMAGE=$(DAPR_REGISTRY)/$(DAPR_OPERATOR_IMAGE_NAME)
DAPR_INJECTOR_DOCKER_IMAGE=$(DAPR_REGISTRY)/$(DAPR_INJECTOR_IMAGE_NAME)
BUILD_TAG=$(DAPR_TAG)-$(DOCKER_IMAGE_VARIANT)

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

docker-build: SHELL := $(shell which bash)
docker-build: check-docker-env check-arch
	$(info Building $(DOCKER_IMAGE):$(DAPR_TAG) docker images ...)
ifeq ($(TARGET_ARCH),$(TARGET_ARCH_LOCAL))
ifeq ($(ONLY_DAPR_IMAGE),true)
	$(DOCKER) build --build-arg PKG_FILES=* $(BUILD_ARGS) -f $(DOCKERFILE_DIR)/$(DOCKERFILE) $(BIN_PATH) -t $(DOCKER_IMAGE):$(BUILD_TAG)
else
	$(DOCKER) build --build-arg PKG_FILES=* $(BUILD_ARGS) -f $(DOCKERFILE_DIR)/$(DOCKERFILE) $(BIN_PATH) -t $(DOCKER_IMAGE):$(BUILD_TAG)
	if [[ "$(BINARIES)" == *"daprd"* ]]; then \
		$(DOCKER) build --build-arg PKG_FILES=daprd $(BUILD_ARGS) -f $(DOCKERFILE_DIR)/$(DOCKERFILE) $(BIN_PATH) -t $(DAPR_RUNTIME_DOCKER_IMAGE):$(BUILD_TAG); \
	fi
	if [[ "$(BINARIES)" == *"placement"* ]]; then \
		$(DOCKER) build --build-arg PKG_FILES=placement $(BUILD_ARGS) -f $(DOCKERFILE_DIR)/$(DOCKERFILE) $(BIN_PATH) -t $(DAPR_PLACEMENT_DOCKER_IMAGE):$(BUILD_TAG); \
	fi
	if [[ "$(BINARIES)" == *"sentry"* ]]; then \
		$(DOCKER) build --build-arg PKG_FILES=sentry $(BUILD_ARGS) -f $(DOCKERFILE_DIR)/$(DOCKERFILE) $(BIN_PATH) -t $(DAPR_SENTRY_DOCKER_IMAGE):$(BUILD_TAG); \
	fi
	if [[ "$(BINARIES)" == *"operator"* ]]; then \
		$(DOCKER) build --build-arg PKG_FILES=operator $(BUILD_ARGS) -f $(DOCKERFILE_DIR)/$(DOCKERFILE) $(BIN_PATH) -t $(DAPR_OPERATOR_DOCKER_IMAGE):$(BUILD_TAG); \
	fi
	if [[ "$(BINARIES)" == *"injector"* ]]; then \
		$(DOCKER) build --build-arg PKG_FILES=injector $(BUILD_ARGS) -f $(DOCKERFILE_DIR)/$(DOCKERFILE) $(BIN_PATH) -t $(DAPR_INJECTOR_DOCKER_IMAGE):$(BUILD_TAG); \
	fi
endif
else
	-$(DOCKER) buildx create --use --name daprbuild
	-$(DOCKER) run --rm --privileged multiarch/qemu-user-static --reset -p yes
ifeq ($(ONLY_DAPR_IMAGE),true)
	$(DOCKER) buildx build --build-arg PKG_FILES=* $(BUILD_ARGS) --platform $(DOCKER_IMAGE_PLATFORM) -f $(DOCKERFILE_DIR)/$(DOCKERFILE) $(BIN_PATH) -t $(DOCKER_IMAGE):$(BUILD_TAG) --provenance=false
else
	$(DOCKER) buildx build --build-arg PKG_FILES=* $(BUILD_ARGS) --platform $(DOCKER_IMAGE_PLATFORM) -f $(DOCKERFILE_DIR)/$(DOCKERFILE) $(BIN_PATH) -t $(DOCKER_IMAGE):$(BUILD_TAG) --provenance=false
	if [[ "$(BINARIES)" == *"daprd"* ]]; then \
		$(DOCKER) buildx build --build-arg PKG_FILES=daprd $(BUILD_ARGS) --platform $(DOCKER_IMAGE_PLATFORM) -f $(DOCKERFILE_DIR)/$(DOCKERFILE) $(BIN_PATH) -t $(DAPR_RUNTIME_DOCKER_IMAGE):$(BUILD_TAG) --provenance=false; \
	fi
	if [[ "$(BINARIES)" == *"placement"* ]]; then \
		$(DOCKER) buildx build --build-arg PKG_FILES=placement $(BUILD_ARGS) --platform $(DOCKER_IMAGE_PLATFORM) -f $(DOCKERFILE_DIR)/$(DOCKERFILE) $(BIN_PATH) -t $(DAPR_PLACEMENT_DOCKER_IMAGE):$(BUILD_TAG) --provenance=false; \
	fi
	if [[ "$(BINARIES)" == *"sentry"* ]]; then \
		$(DOCKER) buildx build --build-arg PKG_FILES=sentry $(BUILD_ARGS) --platform $(DOCKER_IMAGE_PLATFORM) -f $(DOCKERFILE_DIR)/$(DOCKERFILE) $(BIN_PATH) -t $(DAPR_SENTRY_DOCKER_IMAGE):$(BUILD_TAG) --provenance=false; \
	fi
	if [[ "$(BINARIES)" == *"operator"* ]]; then \
		$(DOCKER) buildx build --build-arg PKG_FILES=operator $(BUILD_ARGS) --platform $(DOCKER_IMAGE_PLATFORM) -f $(DOCKERFILE_DIR)/$(DOCKERFILE) $(BIN_PATH) -t $(DAPR_OPERATOR_DOCKER_IMAGE):$(BUILD_TAG) --provenance=false; \
	fi
	if [[ "$(BINARIES)" == *"injector"* ]]; then \
		$(DOCKER) buildx build --build-arg PKG_FILES=injector $(BUILD_ARGS) --platform $(DOCKER_IMAGE_PLATFORM) -f $(DOCKERFILE_DIR)/$(DOCKERFILE) $(BIN_PATH) -t $(DAPR_INJECTOR_DOCKER_IMAGE):$(BUILD_TAG) --provenance=false; \
	fi
endif
endif

# push docker image to the registry
docker-push: SHELL := $(shell which bash)
docker-push: docker-build
	$(info Pushing $(DOCKER_IMAGE):$(DAPR_TAG) docker images ...)
ifeq ($(TARGET_ARCH),$(TARGET_ARCH_LOCAL))
ifeq ($(ONLY_DAPR_IMAGE),true)
	$(DOCKER) push $(DOCKER_IMAGE):$(BUILD_TAG)
else
	$(DOCKER) push $(DOCKER_IMAGE):$(BUILD_TAG)
	if [[ "$(BINARIES)" == *"daprd"* ]]; then \
		$(DOCKER) push $(DAPR_RUNTIME_DOCKER_IMAGE):$(BUILD_TAG); \
	fi
	if [[ "$(BINARIES)" == *"placement"* ]]; then \
		$(DOCKER) push $(DAPR_PLACEMENT_DOCKER_IMAGE):$(BUILD_TAG); \
	fi
	if [[ "$(BINARIES)" == *"sentry"* ]]; then \
		$(DOCKER) push $(DAPR_SENTRY_DOCKER_IMAGE):$(BUILD_TAG); \
	fi
	if [[ "$(BINARIES)" == *"operator"* ]]; then \
		$(DOCKER) push $(DAPR_OPERATOR_DOCKER_IMAGE):$(BUILD_TAG); \
	fi
	if [[ "$(BINARIES)" == *"injector"* ]]; then \
		$(DOCKER) push $(DAPR_INJECTOR_DOCKER_IMAGE):$(BUILD_TAG); \
	fi
endif
else
	-$(DOCKER) buildx create --use --name daprbuild
	-$(DOCKER) run --rm --privileged multiarch/qemu-user-static --reset -p yes
ifeq ($(ONLY_DAPR_IMAGE),true)
	$(DOCKER) buildx build --build-arg PKG_FILES=* --platform $(DOCKER_IMAGE_PLATFORM) -f $(DOCKERFILE_DIR)/$(DOCKERFILE) $(BIN_PATH) -t $(DOCKER_IMAGE):$(BUILD_TAG) --provenance=false --push
else
	$(DOCKER) buildx build --build-arg PKG_FILES=* --platform $(DOCKER_IMAGE_PLATFORM) -f $(DOCKERFILE_DIR)/$(DOCKERFILE) $(BIN_PATH) -t $(DOCKER_IMAGE):$(BUILD_TAG) --provenance=false --push
	if [[ "$(BINARIES)" == *"daprd"* ]]; then \
		$(DOCKER) buildx build --build-arg PKG_FILES=daprd --platform $(DOCKER_IMAGE_PLATFORM) -f $(DOCKERFILE_DIR)/$(DOCKERFILE) $(BIN_PATH) -t $(DAPR_RUNTIME_DOCKER_IMAGE):$(BUILD_TAG) --provenance=false --push; \
	fi
	if [[ "$(BINARIES)" == *"placement"* ]]; then \
		$(DOCKER) buildx build --build-arg PKG_FILES=placement --platform $(DOCKER_IMAGE_PLATFORM) -f $(DOCKERFILE_DIR)/$(DOCKERFILE) $(BIN_PATH) -t $(DAPR_PLACEMENT_DOCKER_IMAGE):$(BUILD_TAG) --provenance=false --push; \
	fi
	if [[ "$(BINARIES)" == *"sentry"* ]]; then \
		$(DOCKER) buildx build --build-arg PKG_FILES=sentry --platform $(DOCKER_IMAGE_PLATFORM) -f $(DOCKERFILE_DIR)/$(DOCKERFILE) $(BIN_PATH) -t $(DAPR_SENTRY_DOCKER_IMAGE):$(BUILD_TAG) --provenance=false --push; \
	fi
	if [[ "$(BINARIES)" == *"operator"* ]]; then \
		$(DOCKER) buildx build --build-arg PKG_FILES=operator --platform $(DOCKER_IMAGE_PLATFORM) -f $(DOCKERFILE_DIR)/$(DOCKERFILE) $(BIN_PATH) -t $(DAPR_OPERATOR_DOCKER_IMAGE):$(BUILD_TAG) --provenance=false --push; \
	fi
	if [[ "$(BINARIES)" == *"injector"* ]]; then \
		$(DOCKER) buildx build --build-arg PKG_FILES=injector --platform $(DOCKER_IMAGE_PLATFORM) -f $(DOCKERFILE_DIR)/$(DOCKERFILE) $(BIN_PATH) -t $(DAPR_INJECTOR_DOCKER_IMAGE):$(BUILD_TAG) --provenance=false --push; \
	fi
endif
endif

# push docker image to kind cluster
docker-push-kind: SHELL := $(shell which bash)
docker-push-kind: docker-build
	$(info Pushing $(DOCKER_IMAGE_TAG) docker image to kind cluster...)
ifeq ($(ONLY_DAPR_IMAGE),true)
	kind load docker-image $(DOCKER_IMAGE):$(BUILD_TAG)
else
	kind load docker-image $(DOCKER_IMAGE):$(BUILD_TAG)
	if [[ "$(BINARIES)" == *"daprd"* ]]; then \
		kind load docker-image $(DAPR_RUNTIME_DOCKER_IMAGE):$(BUILD_TAG); \
	fi
	if [[ "$(BINARIES)" == *"placement"* ]]; then \
		kind load docker-image $(DAPR_PLACEMENT_DOCKER_IMAGE):$(BUILD_TAG); \
	fi
	if [[ "$(BINARIES)" == *"sentry"* ]]; then \
		kind load docker-image $(DAPR_SENTRY_DOCKER_IMAGE):$(BUILD_TAG); \
	fi
	if [[ "$(BINARIES)" == *"operator"* ]]; then \
		kind load docker-image $(DAPR_OPERATOR_DOCKER_IMAGE):$(BUILD_TAG); \
	fi
	if [[ "$(BINARIES)" == *"injector"* ]]; then \
		kind load docker-image $(DAPR_INJECTOR_DOCKER_IMAGE):$(BUILD_TAG); \
	fi
endif

# publish muti-arch docker image to the registry
docker-manifest-create: SHELL := $(shell which bash)
docker-manifest-create: check-docker-env
ifeq ($(ONLY_DAPR_IMAGE),true)
	$(DOCKER) manifest create $(DOCKER_IMAGE):$(DAPR_TAG) $(DOCKER_MULTI_ARCH:%=$(DOCKER_IMAGE):$(MANIFEST_TAG)-%)
else
	$(DOCKER) manifest create $(DOCKER_IMAGE):$(DAPR_TAG) $(DOCKER_MULTI_ARCH:%=$(DOCKER_IMAGE):$(MANIFEST_TAG)-%)
	if [[ "$(BINARIES)" == *"daprd"* ]]; then \
	$(DOCKER) manifest create $(DAPR_RUNTIME_DOCKER_IMAGE):$(DAPR_TAG) $(DOCKER_MULTI_ARCH:%=$(DAPR_RUNTIME_DOCKER_IMAGE):$(MANIFEST_TAG)-%); \
	fi
	if [[ "$(BINARIES)" == *"placement"* ]]; then \
	$(DOCKER) manifest create $(DAPR_PLACEMENT_DOCKER_IMAGE):$(DAPR_TAG) $(DOCKER_MULTI_ARCH:%=$(DAPR_PLACEMENT_DOCKER_IMAGE):$(MANIFEST_TAG)-%); \
	fi
	if [[ "$(BINARIES)" == *"sentry"* ]]; then \
	$(DOCKER) manifest create $(DAPR_SENTRY_DOCKER_IMAGE):$(DAPR_TAG) $(DOCKER_MULTI_ARCH:%=$(DAPR_SENTRY_DOCKER_IMAGE):$(MANIFEST_TAG)-%); \
	fi
	if [[ "$(BINARIES)" == *"operator"* ]]; then \
	$(DOCKER) manifest create $(DAPR_OPERATOR_DOCKER_IMAGE):$(DAPR_TAG) $(DOCKER_MULTI_ARCH:%=$(DAPR_OPERATOR_DOCKER_IMAGE):$(MANIFEST_TAG)-%); \
	fi
	if [[ "$(BINARIES)" == *"injector"* ]]; then \
	$(DOCKER) manifest create $(DAPR_INJECTOR_DOCKER_IMAGE):$(DAPR_TAG) $(DOCKER_MULTI_ARCH:%=$(DAPR_INJECTOR_DOCKER_IMAGE):$(MANIFEST_TAG)-%); \
	fi
endif
ifeq ($(LATEST_RELEASE),true)
ifeq ($(ONLY_DAPR_IMAGE),true)
	$(DOCKER) manifest create $(DOCKER_IMAGE):$(LATEST_TAG) $(DOCKER_MULTI_ARCH:%=$(DOCKER_IMAGE):$(MANIFEST_LATEST_TAG)-%)
else
	$(DOCKER) manifest create $(DOCKER_IMAGE):$(LATEST_TAG) $(DOCKER_MULTI_ARCH:%=$(DOCKER_IMAGE):$(MANIFEST_LATEST_TAG)-%)
	if [[ "$(BINARIES)" == *"daprd"* ]]; then \
	$(DOCKER) manifest create $(DAPR_RUNTIME_DOCKER_IMAGE):$(LATEST_TAG) $(DOCKER_MULTI_ARCH:%=$(DAPR_RUNTIME_DOCKER_IMAGE):$(MANIFEST_LATEST_TAG)-%); \
	fi
	if [[ "$(BINARIES)" == *"placement"* ]]; then \
	$(DOCKER) manifest create $(DAPR_PLACEMENT_DOCKER_IMAGE):$(LATEST_TAG) $(DOCKER_MULTI_ARCH:%=$(DAPR_PLACEMENT_DOCKER_IMAGE):$(MANIFEST_LATEST_TAG)-%); \
	fi
	if [[ "$(BINARIES)" == *"sentry"* ]]; then \
	$(DOCKER) manifest create $(DAPR_SENTRY_DOCKER_IMAGE):$(LATEST_TAG) $(DOCKER_MULTI_ARCH:%=$(DAPR_SENTRY_DOCKER_IMAGE):$(MANIFEST_LATEST_TAG)-%); \
	fi
	if [[ "$(BINARIES)" == *"operator"* ]]; then \
	$(DOCKER) manifest create $(DAPR_OPERATOR_DOCKER_IMAGE):$(LATEST_TAG) $(DOCKER_MULTI_ARCH:%=$(DAPR_OPERATOR_DOCKER_IMAGE):$(MANIFEST_LATEST_TAG)-%); \
	fi
	if [[ "$(BINARIES)" == *"injector"* ]]; then \
	$(DOCKER) manifest create $(DAPR_INJECTOR_DOCKER_IMAGE):$(LATEST_TAG) $(DOCKER_MULTI_ARCH:%=$(DAPR_INJECTOR_DOCKER_IMAGE):$(MANIFEST_LATEST_TAG)-%); \
	fi
endif
endif

docker-publish: SHELL := $(shell which bash)
docker-publish: docker-manifest-create
ifeq ($(ONLY_DAPR_IMAGE),true)
	$(DOCKER) manifest push $(DOCKER_IMAGE):$(DAPR_TAG)
else
	$(DOCKER) manifest push $(DOCKER_IMAGE):$(DAPR_TAG)
	if [[ "$(BINARIES)" == *"daprd"* ]]; then \
	$(DOCKER) manifest push $(DAPR_RUNTIME_DOCKER_IMAGE):$(DAPR_TAG); \
	fi
	if [[ "$(BINARIES)" == *"placement"* ]]; then \
	$(DOCKER) manifest push $(DAPR_PLACEMENT_DOCKER_IMAGE):$(DAPR_TAG); \
	fi
	if [[ "$(BINARIES)" == *"sentry"* ]]; then \
	$(DOCKER) manifest push $(DAPR_SENTRY_DOCKER_IMAGE):$(DAPR_TAG); \
	fi
	if [[ "$(BINARIES)" == *"operator"* ]]; then \
	$(DOCKER) manifest push $(DAPR_OPERATOR_DOCKER_IMAGE):$(DAPR_TAG); \
	fi
	if [[ "$(BINARIES)" == *"injector"* ]]; then \
	$(DOCKER) manifest push $(DAPR_INJECTOR_DOCKER_IMAGE):$(DAPR_TAG); \
	fi
endif
ifeq ($(LATEST_RELEASE),true)
ifeq ($(ONLY_DAPR_IMAGE),true)
	$(DOCKER) manifest push $(DOCKER_IMAGE):$(LATEST_TAG)
else
	$(DOCKER) manifest push $(DOCKER_IMAGE):$(LATEST_TAG)
	if [[ "$(BINARIES)" == *"daprd"* ]]; then \
	$(DOCKER) manifest push $(DAPR_RUNTIME_DOCKER_IMAGE):$(LATEST_TAG); \
	fi
	if [[ "$(BINARIES)" == *"placement"* ]]; then \
	$(DOCKER) manifest push $(DAPR_PLACEMENT_DOCKER_IMAGE):$(LATEST_TAG); \
	fi
	if [[ "$(BINARIES)" == *"sentry"* ]]; then \
	$(DOCKER) manifest push $(DAPR_SENTRY_DOCKER_IMAGE):$(LATEST_TAG); \
	fi
	if [[ "$(BINARIES)" == *"operator"* ]]; then \
	$(DOCKER) manifest push $(DAPR_OPERATOR_DOCKER_IMAGE):$(LATEST_TAG); \
	fi
	if [[ "$(BINARIES)" == *"injector"* ]]; then \
	$(DOCKER) manifest push $(DAPR_INJECTOR_DOCKER_IMAGE):$(LATEST_TAG); \
	fi
endif
endif

check-windows-version:
ifeq ($(WINDOWS_VERSION),)
	$(error WINDOWS_VERSION environment variable must be set)
endif

docker-windows-base-build: check-windows-version
	$(DOCKER) build --build-arg WINDOWS_VERSION=$(WINDOWS_VERSION) -f $(DOCKERFILE_DIR)/$(DOCKERFILE)-base $(DOCKERFILE_DIR) -t $(DAPR_REGISTRY)/windows-base:$(WINDOWS_VERSION)
	$(DOCKER) build --build-arg WINDOWS_VERSION=$(WINDOWS_VERSION) -f $(DOCKERFILE_DIR)/$(DOCKERFILE)-php-base $(DOCKERFILE_DIR) -t $(DAPR_REGISTRY)/windows-php-base:$(WINDOWS_VERSION)
	$(DOCKER) build --build-arg WINDOWS_VERSION=$(WINDOWS_VERSION) -f $(DOCKERFILE_DIR)/$(DOCKERFILE)-python-base $(DOCKERFILE_DIR) -t $(DAPR_REGISTRY)/windows-python-base:$(WINDOWS_VERSION)

docker-windows-base-push: check-windows-version
	$(DOCKER) push $(DAPR_REGISTRY)/windows-base:$(WINDOWS_VERSION)
	$(DOCKER) push $(DAPR_REGISTRY)/windows-php-base:$(WINDOWS_VERSION)
	$(DOCKER) push $(DAPR_REGISTRY)/windows-python-base:$(WINDOWS_VERSION)

################################################################################
# Target: build-dev-container, push-dev-container                              #
################################################################################

# Update whenever you upgrade dev container image
DEV_CONTAINER_VERSION_TAG?=latest

# Use this to pin a specific version of the Dapr CLI to a devcontainer
DEV_CONTAINER_CLI_TAG?=1.9.0

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

build-dev-container-all-arch:
ifeq ($(DAPR_REGISTRY),)
	$(info DAPR_REGISTRY environment variable not set, tagging image without registry prefix.)
	$(DOCKER) buildx build \
		--build-arg DAPR_CLI_VERSION=$(DEV_CONTAINER_CLI_TAG) \
		-f $(DOCKERFILE_DIR)/$(DEV_CONTAINER_DOCKERFILE) \
		--platform linux/amd64,linux/arm64 \
		-t $(DEV_CONTAINER_IMAGE_NAME):$(DEV_CONTAINER_VERSION_TAG) \
		$(DOCKERFILE_DIR)/. \
		--provenance=false
else
	$(DOCKER) buildx build \
		--build-arg DAPR_CLI_VERSION=$(DEV_CONTAINER_CLI_TAG) \
		-f $(DOCKERFILE_DIR)/$(DEV_CONTAINER_DOCKERFILE) \
		--platform linux/amd64,linux/arm64 \
		-t $(DAPR_REGISTRY)/$(DEV_CONTAINER_IMAGE_NAME):$(DEV_CONTAINER_VERSION_TAG) \
		$(DOCKERFILE_DIR)/. \
		--provenance=false
endif

push-dev-container-all-arch: check-docker-env-for-dev-container
	$(DOCKER) buildx build \
		--build-arg DAPR_CLI_VERSION=$(DEV_CONTAINER_CLI_TAG) \
		-f $(DOCKERFILE_DIR)/$(DEV_CONTAINER_DOCKERFILE) \
		--platform linux/amd64,linux/arm64 \
		--push \
		-t $(DAPR_REGISTRY)/$(DEV_CONTAINER_IMAGE_NAME):$(DEV_CONTAINER_VERSION_TAG) \
		$(DOCKERFILE_DIR)/. \
		--provenance=false
