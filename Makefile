################################################################################
# Variables																       #
################################################################################

export GO111MODULE ?= on
export GOPROXY ?= https://proxy.golang.org
export GOSUMDB ?= sum.golang.org

GIT_COMMIT  = $(shell git rev-list -1 HEAD)
GIT_VERSION = $(shell git describe --always --abbrev=7 --dirty)
CGO			?= 0
BINARIES    ?= actionsrt placement operator injector

# Add latest tag if LATEST_RELEASE is true
LATEST_RELEASE ?=

ifdef REL_VERSION
	ACTIONS_VERSION := $(REL_VERSION)
else
	ACTIONS_VERSION := edge
endif

LOCAL_ARCH := $(shell uname -m)
ifeq ($(LOCAL_ARCH),x86_64)
	TARGET_ARCH_LOCAL = amd64
else ifeq ($(shell echo $(LOCAL_ARCH) | head -c 5),armv8)
	TARGET_ARCH_LOCAL = arm64
else ifeq ($(shell echo $(LOCAL_ARCH) | head -c 4),armv)
	TARGET_ARCH_LOCAL = arm
else
	TARGET_ARCH_LOCAL = amd64
endif
export GOARCH ?= $(TARGET_ARCH_LOCAL)

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

# Docker image build and push setting
DOCKER:=docker
DOCKERFILE_DIR?=./docker
DOCKERFILE:=Dockerfile

ifeq ($(origin DEBUG), undefined)
  BUILDTYPE_DIR:=release
else ifeq ($(DEBUG),0)
  BUILDTYPE_DIR:=release
else
  DOCKERFILE:=Dockerfile-debug
  BUILDTYPE_DIR:=debug
  GCFLAGS:=-gcflags="all=-N -l"
  $(info Build with debugger information)
endif

# Helm template and install setting
HELM:=helm
RELEASE_NAME?=actions
HELM_NAMESPACE?=actions-system
HELM_CHART_DIR:=./charts/actions-operator
HELM_OUT_DIR:=$(OUT_DIR)/install
HELM_MANIFEST_FILE:=$(HELM_OUT_DIR)/$(RELEASE_NAME).yaml

################################################################################
# Go build details                                                             #
################################################################################
BASE_PACKAGE_NAME := github.com/actionscore/actions
OUT_DIR := ./dist

ACTIONS_OUT_DIR := $(OUT_DIR)/$(GOOS)_$(GOARCH)/$(BUILDTYPE_DIR)
ACTIONS_LINUX_OUT_DIR := $(OUT_DIR)/linux_$(GOARCH)/$(BUILDTYPE_DIR)
LDFLAGS := "-X $(BASE_PACKAGE_NAME)/pkg/version.commit=$(GIT_VERSION) -X $(BASE_PACKAGE_NAME)/pkg/version.version=$(ACTIONS_VERSION)"

################################################################################
# Target: build                                                                #
################################################################################
.PHONY: build
ACTIONS_BINS:=$(foreach ITEM,$(BINARIES),$(ACTIONS_OUT_DIR)/$(ITEM)$(BINARY_EXT))
build: $(ACTIONS_BINS)

# Generate builds for actions binaries for the target
# Params:
# $(1): the binary name for the target
# $(2): the binary main directory
# $(3): the target os
# $(4): the target arch
# $(5): the output directory
define genBinariesForTarget
.PHONY: $(5)/$(1)
$(5)/$(1):
	CGO_ENABLED=$(CGO) GOOS=$(3) GOARCH=$(4) go build $(GCFLAGS) -ldflags $(LDFLAGS) \
	-o $(5)/$(1) -mod=vendor \
	$(2)/main.go;
endef

# Generate binary targets
$(foreach ITEM,$(BINARIES),$(eval $(call genBinariesForTarget,$(ITEM)$(BINARY_EXT),./cmd/$(ITEM),$(GOOS),$(GOARCH),$(ACTIONS_OUT_DIR))))

################################################################################
# Target: build-linux                                                          #
################################################################################
BUILD_LINUX_BINS:=$(foreach ITEM,$(BINARIES),$(ACTIONS_LINUX_OUT_DIR)/$(ITEM))
build-linux: $(BUILD_LINUX_BINS)

# Generate linux binaries targets to build linux docker image
ifneq ($(GOOS), linux)
$(foreach ITEM,$(BINARIES),$(eval $(call genBinariesForTarget,$(ITEM),./cmd/$(ITEM),linux,$(GOARCH),$(ACTIONS_LINUX_OUT_DIR))))
endif

################################################################################
# Target: archive                                                              #
################################################################################
ARCHIVE_OUT_DIR ?= $(ACTIONS_OUT_DIR)
ARCHIVE_FILE_EXTS:=$(foreach ITEM,$(BINARIES),archive-$(ITEM)$(ARCHIVE_EXT))

archive: $(ARCHIVE_FILE_EXTS)

# Generate archive files for each binary
# $(1): the binary name to be archived
# $(2): the archived file output directory
define genArchiveBinary
ifeq ($(GOOS),windows)
archive-$(1).zip:
	7z.exe a -tzip "$(2)\\$(1)_$(GOOS)_$(GOARCH)$(ARCHIVE_EXT)" "$(ACTIONS_OUT_DIR)\\$(1)$(BINARY_EXT)"
else
archive-$(1).tar.gz:
	tar czf "$(2)/$(1)_$(GOOS)_$(GOARCH)$(ARCHIVE_EXT)" -C "$(ACTIONS_OUT_DIR)" "$(1)$(BINARY_EXT)"
endif
endef

# Generate archive-*.[zip|tar.gz] targets
$(foreach ITEM,$(BINARIES),$(eval $(call genArchiveBinary,$(ITEM),$(ARCHIVE_OUT_DIR))))

################################################################################
# Target: docker-build, docker-push                                            #
################################################################################

LINUX_BINS_OUT_DIR=$(OUT_DIR)/linux_$(GOARCH)
DOCKER_IMAGE_TAG=$(ACTIONS_REGISTRY)/$(RELEASE_NAME):$(ACTIONS_TAG)

ifeq ($(LATEST_RELEASE),true)
DOCKER_IMAGE_LATEST_TAG=$(ACTIONS_REGISTRY)/$(RELEASE_NAME):latest
endif

# check the required environment variables
check-docker-env:
ifeq ($(ACTIONS_REGISTRY),)
	$(error ACTIONS_REGISTRY environment variable must be set)
endif
ifeq ($(ACTIONS_TAG),)
	$(error ACTIONS_TAG environment variable must be set)
endif

# build docker image for linux
docker-build: check-docker-env build-linux
	$(info Building $(DOCKER_IMAGE_TAG) docker image ...)
	$(DOCKER) build -f $(DOCKERFILE_DIR)/$(DOCKERFILE) $(LINUX_BINS_OUT_DIR)/. -t $(DOCKER_IMAGE_TAG)
ifeq ($(LATEST_RELEASE),true)
	$(info Building $(DOCKER_IMAGE_LATEST_TAG) docker image ...)
	$(DOCKER) tag $(DOCKER_IMAGE_TAG) $(DOCKER_IMAGE_LATEST_TAG)
endif

# push docker image to the registry
docker-push: docker-build
	$(info Pushing $(DOCKER_IMAGE_TAG) docker image ...)
	$(DOCKER) push $(DOCKER_IMAGE_TAG)
ifeq ($(LATEST_RELEASE),true)
	$(info Pushing $(DOCKER_IMAGE_LATEST_TAG) docker image ...)
	$(DOCKER) push $(DOCKER_IMAGE_LATEST_TAG)
endif

################################################################################
# Target: manifest-gen                                                         #
################################################################################

# Generate helm chart manifest
manifest-gen: actions-operator.yaml

$(HOME)/.helm:
	$(HELM) init --client-only

actions-operator.yaml: check-docker-env $(HOME)/.helm
	$(info Generating helm manifest $(HELM_MANIFEST_FILE)...)
	@mkdir -p $(HELM_OUT_DIR)
	$(HELM) template \
		--name=$(RELEASE_NAME) \
		--namespace=$(HELM_NAMESPACE) \
		--set-string global.tag=${ACTIONS_TAG} \ 
		--set-string global.registry=${ACTIONS_REGISTRY} \
		$(HELM_CHART_DIR) > $(HELM_MANIFEST_FILE)

################################################################################
# Target: docker-deploy-k8s                                                    #
################################################################################

docker-deploy-k8s: check-docker-env $(HOME)/.helm
	$(info Deploying ${ACTIONS_REGISTRY}/${RELEASE_NAME}:${ACTIONS_TAG} to the current K8S context...)
	$(HELM) install \
		--name=$(RELEASE_NAME) \
		--namespace=$(HELM_NAMESPACE) \
		--set-string global.tag=${ACTIONS_TAG}
		--set-string global.registry=${ACTIONS_REGISTRY} \
		$(HELM_CHART_DIR)

################################################################################
# Target: archive                                                              #
################################################################################
release: build archive

################################################################################
# Target: test															       #
################################################################################
.PHONY: test
test:
	go test ./pkg/... -mod=vendor
