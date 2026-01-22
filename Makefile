# ------------------------------------------------------------
# Copyright (c) Microsoft Corporation and Dapr Contributors..
# Licensed under the MIT License.
# ------------------------------------------------------------

################################################################################
# Variables                                                                    #
################################################################################

export GO111MODULE ?= on
export GOPROXY ?= https://proxy.golang.org
export GOSUMDB ?= sum.golang.org

PROJECT_ROOT := infobloxopen/dapr
REPO         := infoblox
GITHUB_REPO  := git@github.com:infobloxopen
WINDOWS_VERSION :=1809


GIT_COMMIT  = $(shell git describe --tag --dirty=-unsupported --always || echo pre-commit)
GIT_VERSION = $(shell git describe --always --abbrev=7 )
# By default, disable CGO_ENABLED. See the details on https://golang.org/cmd/cgo
CGO         ?= 0
BINARIES    ?= daprd placement operator injector sentry
GIT_COMMIT_NUMBER = $(shell git rev-parse --short HEAD)
# Add latest tag if LATEST_RELEASE is true
LATEST_RELEASE ?=


DAPR_VERSION = v1.0.0-ib-$(GIT_COMMIT_NUMBER)

LOCAL_ARCH := $(shell uname -m)
ifeq ($(LOCAL_ARCH),x86_64)
	TARGET_ARCH_LOCAL=amd64
else ifeq ($(shell echo $(LOCAL_ARCH) | head -c 5),armv8)
	TARGET_ARCH_LOCAL=arm64
else ifeq ($(shell echo $(LOCAL_ARCH) | head -c 4),armv)
	TARGET_ARCH_LOCAL=arm
else
	TARGET_ARCH_LOCAL=amd64
endif
export GOARCH ?= $(TARGET_ARCH_LOCAL)

ifeq ($(GOARCH),amd64)
	LATEST_TAG=latest
else
	LATEST_TAG=latest-$(GOARCH)
endif

LOCAL_OS := $(shell uname)
ifeq ($(LOCAL_OS),Linux)
   TARGET_OS_LOCAL = linux
else ifeq ($(LOCAL_OS),Darwin)
   TARGET_OS_LOCAL = darwin
else
   TARGET_OS_LOCAL ?= windows
endif
export GOOS ?= $(TARGET_OS_LOCAL)

ifeq ($(GOOS),windows)
BINARY_EXT_LOCAL:=.exe
export ARCHIVE_EXT = .zip
else
BINARY_EXT_LOCAL:=
export ARCHIVE_EXT = .tar.gz
endif

export BINARY_EXT ?= $(BINARY_EXT_LOCAL)

OUT_DIR := ./dist

# Docker image build and push setting
DOCKER:=docker
DOCKERFILE_DIR?=./docker
DOCKERFILE:=Dockerfile

# Helm template and install setting
HELM:=helm
RELEASE_NAME?=dapr
HELM_NAMESPACE?=dapr-system
HELM_CHART_ROOT:=./charts
HELM_CHART_DIR:=$(HELM_CHART_ROOT)/dapr
HELM_OUT_DIR:=$(OUT_DIR)/install
HELM_MANIFEST_FILE:=$(HELM_CHART_ROOT)/manifest/$(RELEASE_NAME).yaml

################################################################################
# Go build details                                                             #
################################################################################
BASE_PACKAGE_NAME := github.com/dapr/dapr

DEFAULT_LDFLAGS:=-X $(BASE_PACKAGE_NAME)/pkg/version.commit=$(GIT_VERSION) -X $(BASE_PACKAGE_NAME)/pkg/version.version=$(DAPR_VERSION)

ifeq ($(origin DEBUG), undefined)
  BUILDTYPE_DIR:=release
  LDFLAGS:="$(DEFAULT_LDFLAGS) -s -w"
else ifeq ($(DEBUG),0)
  BUILDTYPE_DIR:=release
  LDFLAGS:="$(DEFAULT_LDFLAGS) -s -w"
else
  DOCKERFILE:=Dockerfile-debug
  BUILDTYPE_DIR:=debug
  GCFLAGS:=-gcflags="all=-N -l"
  LDFLAGS:="$(DEFAULT_LDFLAGS)"
  $(info Build with debugger information)
endif

DAPR_OUT_DIR := $(OUT_DIR)/$(GOOS)_$(GOARCH)/$(BUILDTYPE_DIR)
DAPR_LINUX_OUT_DIR := $(OUT_DIR)/linux_$(GOARCH)/$(BUILDTYPE_DIR)

################################################################################
# Target: build                                                                #
################################################################################
.PHONY: build
DAPR_BINS:=$(foreach ITEM,$(BINARIES),$(DAPR_OUT_DIR)/$(ITEM)$(BINARY_EXT))
build: $(DAPR_BINS)

# Generate builds for dapr binaries for the target
# Params:
# $(1): the binary name for the target
# $(2): the binary main directory
# $(3): the target os
# $(4): the target arch
# $(5): the output directory
define genBinariesForTarget
.PHONY: $(5)/$(1)
$(5)/$(1):
	CGO_ENABLED=$(CGO) GOOS=$(3) GOARCH=$(4) go build $(GCFLAGS) -ldflags=$(LDFLAGS) \
	-o $(5)/$(1) $(2)/;
endef

# Generate binary targets
$(foreach ITEM,$(BINARIES),$(eval $(call genBinariesForTarget,$(ITEM)$(BINARY_EXT),./cmd/$(ITEM),$(GOOS),$(GOARCH),$(DAPR_OUT_DIR))))

################################################################################
# Target: build-linux                                                          #
################################################################################
BUILD_LINUX_BINS:=$(foreach ITEM,$(BINARIES),$(DAPR_LINUX_OUT_DIR)/$(ITEM))
build-linux: $(BUILD_LINUX_BINS)

# Generate linux binaries targets to build linux docker image
ifneq ($(GOOS), linux)
$(foreach ITEM,$(BINARIES),$(eval $(call genBinariesForTarget,$(ITEM),./cmd/$(ITEM),linux,$(GOARCH),$(DAPR_LINUX_OUT_DIR))))
endif

################################################################################
# Target: archive                                                              #
################################################################################
ARCHIVE_OUT_DIR ?= $(DAPR_OUT_DIR)
ARCHIVE_FILE_EXTS:=$(foreach ITEM,$(BINARIES),archive-$(ITEM)$(ARCHIVE_EXT))

archive: $(ARCHIVE_FILE_EXTS)

# Generate archive files for each binary
# $(1): the binary name to be archived
# $(2): the archived file output directory
define genArchiveBinary
ifeq ($(GOOS),windows)
archive-$(1).zip:
	7z.exe a -tzip "$(2)\\$(1)_$(GOOS)_$(GOARCH)$(ARCHIVE_EXT)" "$(DAPR_OUT_DIR)\\$(1)$(BINARY_EXT)"
else
archive-$(1).tar.gz:
	tar czf "$(2)/$(1)_$(GOOS)_$(GOARCH)$(ARCHIVE_EXT)" -C "$(DAPR_OUT_DIR)" "$(1)$(BINARY_EXT)"
endif
endef

# Generate archive-*.[zip|tar.gz] targets
$(foreach ITEM,$(BINARIES),$(eval $(call genArchiveBinary,$(ITEM),$(ARCHIVE_OUT_DIR))))

################################################################################
# Target: docker-build, docker-push                                            #
################################################################################

LINUX_BINS_OUT_DIR=$(OUT_DIR)/linux_$(GOARCH)
DOCKER_IMAGE_TAG=$(REPO)/$(RELEASE_NAME):$(DAPR_VERSION)
DAPR_RUNTIME_DOCKER_IMAGE_NAME=daprd
DAPR_RUNTIME_DOCKER_IMAGE_TAG=$(REPO)/$(DAPR_RUNTIME_DOCKER_IMAGE_NAME):$(DAPR_VERSION)
DAPR_PLACEMENT_DOCKER_IMAGE_NAME=placement
DAPR_PLACEMENT_DOCKER_IMAGE_TAG=$(REPO)/$(DAPR_PLACEMENT_DOCKER_IMAGE_NAME):$(DAPR_VERSION)
DAPR_SENTRY_DOCKER_IMAGE_NAME=sentry
DAPR_SENTRY_DOCKER_IMAGE_TAG=$(REPO)/$(DAPR_SENTRY_DOCKER_IMAGE_NAME):$(DAPR_VERSION)

ifeq ($(LATEST_RELEASE),true)
DOCKER_IMAGE_LATEST_TAG=$(REPO)/$(RELEASE_NAME):$(LATEST_TAG)
DAPR_RUNTIME_DOCKER_IMAGE_LATEST_TAG=$(DAPR_RUNTIME_DOCKER_IMAGE_TAG):$(LATEST_TAG)
DAPR_PLACEMENT_DOCKER_IMAGE_LATEST_TAG=$(DAPR_PLACEMENT_DOCKER_IMAGE_TAG):$(LATEST_TAG)
DAPR_SENTRY_DOCKER_IMAGE_LATEST_TAG=$(DAPR_SENTRY_DOCKER_IMAGE_TAG):$(LATEST_TAG)
endif

show-images:
	@echo "$(DOCKER_IMAGE_TAG) $(DAPR_RUNTIME_DOCKER_IMAGE_TAG) $(DAPR_PLACEMENT_DOCKER_IMAGE_TAG) $(DAPR_SENTRY_DOCKER_IMAGE_TAG)"

# build docker image for linux
docker-build:
ifeq ($(GOARCH),amd64)
	$(info Building $(DOCKER_IMAGE_TAG) docker image ...)
	$(DOCKER) build --build-arg PKG_FILES=* -f $(DOCKERFILE_DIR)/$(DOCKERFILE) $(LINUX_BINS_OUT_DIR)/. -t $(DOCKER_IMAGE_TAG)
	$(info Building $(DAPR_RUNTIME_DOCKER_IMAGE_TAG) docker image ...)
	$(DOCKER) build --build-arg PKG_FILES=daprd -f $(DOCKERFILE_DIR)/$(DOCKERFILE) $(LINUX_BINS_OUT_DIR)/. -t $(DAPR_RUNTIME_DOCKER_IMAGE_TAG)
	$(info Building $(DAPR_PLACEMENT_DOCKER_IMAGE_TAG) docker image ...)
	$(DOCKER) build --build-arg PKG_FILES=placement -f $(DOCKERFILE_DIR)/$(DOCKERFILE) $(LINUX_BINS_OUT_DIR)/. -t $(DAPR_PLACEMENT_DOCKER_IMAGE_TAG)
	$(info Building $(DAPR_SENTRY_DOCKER_IMAGE_TAG) docker image ...)
	$(DOCKER) build --build-arg PKG_FILES=sentry -f $(DOCKERFILE_DIR)/$(DOCKERFILE) $(LINUX_BINS_OUT_DIR)/. -t $(DAPR_SENTRY_DOCKER_IMAGE_TAG)
else
	-$(DOCKER) buildx create --use --name daprbuild
	-$(DOCKER) run --rm --privileged multiarch/qemu-user-static --reset
	$(DOCKER) buildx build --build-arg PKG_FILES=* --platform $(DOCKER_IMAGE_PLATFORM) -f $(DOCKERFILE_DIR)/$(DOCKERFILE) $(LINUX_BINS_OUT_DIR)/. -t $(DOCKER_IMAGE_TAG)
	$(DOCKER) buildx build --build-arg PKG_FILES=daprd --platform $(DOCKER_IMAGE_PLATFORM) -f $(DOCKERFILE_DIR)/$(DOCKERFILE) $(LINUX_BINS_OUT_DIR)/. -t $(DAPR_RUNTIME_DOCKER_IMAGE_TAG)
	$(DOCKER) buildx build --build-arg PKG_FILES=placement --platform $(DOCKER_IMAGE_PLATFORM) -f $(DOCKERFILE_DIR)/$(DOCKERFILE) $(LINUX_BINS_OUT_DIR)/. -t $(DAPR_PLACEMENT_DOCKER_IMAGE_TAG)
	$(DOCKER) buildx build --build-arg PKG_FILES=sentry --platform $(DOCKER_IMAGE_PLATFORM) -f $(DOCKERFILE_DIR)/$(DOCKERFILE) $(LINUX_BINS_OUT_DIR)/. -t $(DAPR_SENTRY_DOCKER_IMAGE_TAG)
endif
ifeq ($(LATEST_RELEASE),true)
	$(info Building $(DOCKER_IMAGE_LATEST_TAG) docker image ...)
	$(DOCKER) tag $(DOCKER_IMAGE_TAG) $(DOCKER_IMAGE_LATEST_TAG)
	$(DOCKER) tag $(DAPR_RUNTIME_DOCKER_IMAGE_TAG) $(DAPR_RUNTIME_DOCKER_IMAGE_LATEST_TAG)
	$(DOCKER) tag $(DAPR_PLACEMENT_DOCKER_IMAGE_TAG) $(DAPR_PLACEMENT_DOCKER_IMAGE_LATEST_TAG)
	$(DOCKER) tag $(DAPR_SENTRY_DOCKER_IMAGE_TAG) $(DAPR_SENTRY_DOCKER_IMAGE_LATEST_TAG)
endif

check-arch-platform:
ifeq ($(GOARCH),arm)
	$(eval DOCKER_IMAGE_PLATFORM := $(GOOS)/arm/v7)
else ifeq ($(GOARCH),arm64)
	$(eval DOCKER_IMAGE_PLATFORM := $(GOOS)/arm64/v8)
endif
# push docker image to the registry
docker-push: check-arch-platform docker-build
ifeq ($(GOARCH),amd64)
	$(info Pushing $(DOCKER_IMAGE_TAG) docker image ...)
	$(DOCKER) push $(DOCKER_IMAGE_TAG)
	$(info Pushing $(DAPR_RUNTIME_DOCKER_IMAGE_TAG) docker image ...)
	$(DOCKER) push $(DAPR_RUNTIME_DOCKER_IMAGE_TAG)
	$(info Pushing $(DAPR_PLACEMENT_DOCKER_IMAGE_TAG) docker image ...)
	$(DOCKER) push $(DAPR_PLACEMENT_DOCKER_IMAGE_TAG)
	$(info Pushing $(DAPR_SENTRY_DOCKER_IMAGE_TAG) docker image ...)
	$(DOCKER) push $(DAPR_SENTRY_DOCKER_IMAGE_TAG)
else
	-$(DOCKER) buildx create --use --name daprbuild
	-$(DOCKER) run --rm --privileged multiarch/qemu-user-static --reset
	$(DOCKER) buildx build --build-arg PKG_FILES=*  -f $(DOCKERFILE_DIR)/$(DOCKERFILE) $(LINUX_BINS_OUT_DIR)  -t $(DOCKER_IMAGE_TAG)  --push --platform $(DOCKER_IMAGE_PLATFORM)
	$(DOCKER) buildx build --build-arg PKG_FILES=daprd  -f $(DOCKERFILE_DIR)/$(DOCKERFILE) $(LINUX_BINS_OUT_DIR)  -t $(DAPR_RUNTIME_DOCKER_IMAGE_TAG)  --push --platform $(DOCKER_IMAGE_PLATFORM)
	$(DOCKER) buildx build --build-arg PKG_FILES=placement  -f $(DOCKERFILE_DIR)/$(DOCKERFILE) $(LINUX_BINS_OUT_DIR)  -t $(DAPR_PLACEMENT_DOCKER_IMAGE_TAG)  --push --platform $(DOCKER_IMAGE_PLATFORM)
	$(DOCKER) buildx build --build-arg PKG_FILES=sentry  -f $(DOCKERFILE_DIR)/$(DOCKERFILE) $(LINUX_BINS_OUT_DIR)  -t $(DAPR_SENTRY_DOCKER_IMAGE_TAG)  --push --platform $(DOCKER_IMAGE_PLATFORM) 
endif
ifeq ($(LATEST_RELEASE),true)
	$(info Pushing $(DOCKER_IMAGE_LATEST_TAG) docker image ...)
	$(DOCKER) push $(DOCKER_IMAGE_LATEST_TAG)
	$(DOCKER) push $(DAPR_RUNTIME_DOCKER_IMAGE_LATEST_TAG)
	$(DOCKER) push $(DAPR_PLACEMENT_DOCKER_IMAGE_LATEST_TAG)
	$(DOCKER) push $(DAPR_SENTRY_DOCKER_IMAGE_LATEST_TAG)
endif

windows-version:
ifeq ($(WINDOWS_VERSION),)
	$(error WINDOWS_VERSION environment variable must be set)
endif

docker-windows-base-build: check-windows-version
	$(DOCKER) build --build-arg WINDOWS_VERSION=$(WINDOWS_VERSION) -f $(DOCKERFILE_DIR)/Dockerfile-windows-base . -t $(REPO)/windows-base:$(WINDOWS_VERSION)

docker-windows-base-push: docker-windows-base-build
	$(DOCKER) push $(REPO)/windows-base:$(WINDOWS_VERSION)

################################################################################
# Target: manifest-gen                                                         #
################################################################################

# Generate helm chart manifest
manifest-gen: dapr.yaml

$(HOME)/.helm:
	$(HELM) init --client-only

dapr.yaml:  $(HOME)/.helm
	$(info Generating helm manifest $(HELM_MANIFEST_FILE)...)
	@mkdir -p $(HELM_OUT_DIR)
	$(HELM) template \
		--set-string global.tag=$(DAPR_VERSION) --set-string global.registry=$(REPO) $(HELM_CHART_DIR) > $(HELM_MANIFEST_FILE)

################################################################################
# Target: docker-deploy-k8s                                                    #
################################################################################

docker-deploy-k8s: $(HOME)/.helm
	$(info Deploying $(REPO)/${RELEASE_NAME}:$(DAPR_VERSION) to the current K8S context...)
	$(HELM) install \
		--name=$(RELEASE_NAME) --namespace=$(HELM_NAMESPACE) \
		--set-string global.tag=$(DAPR_VERSION) --set-string global.registry=$(REPO) $(HELM_CHART_DIR)

################################################################################
# Target: archive                                                              #
################################################################################
release: build archive

################################################################################
# Target:   tidy                                                               #
################################################################################
.PHONY: tidy
tidy:
	go mod tidy
	go mod vendor
################################################################################
# Clean : clean                                                                #
################################################################################
.PHONY:clean
clean:
	$(DOCKER) rmi -f $(shell docker images -q $(DOCKER_IMAGE_TAG))  || true
	$(DOCKER) rmi -f $(shell docker images -q $(DAPR_RUNTIME_DOCKER_IMAGE_TAG)) || true
	$(DOCKER) rmi -f $(shell docker images -q $(DAPR_PLACEMENT_DOCKER_IMAGE_TAG)) || true
	$(DOCKER) rmi -f $(shell docker images -q $(DAPR_SENTRY_DOCKER_IMAGE_TAG)) || true
################################################################################
# Target: test                                                                 #
################################################################################
.PHONY: test
test:
	go test ./pkg/... $(COVERAGE_OPTS)
	go test ./tests/...
