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

DOCKER      = docker
DOCKER_DIR  ?= ./docker
DOCKERFILE  = "Dockerfile"

HELM        = helm

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
   export ARCHIVE_EXT = .tar.gz
else ifeq ($(LOCAL_OS),Darwin)
   TARGET_OS_LOCAL = darwin
   export ARCHIVE_EXT = .tar.gz
else
   TARGET_OS_LOCAL ?= windows
   BINARY_EXT_LOCAL = .exe
   export ARCHIVE_EXT = .zip
endif
export GOOS ?= $(TARGET_OS_LOCAL)
export BINARY_EXT ?= $(BINARY_EXT_LOCAL)

# Use the variable H to add a header (equivalent to =>) to informational output
H = $(shell printf "\033[34;1m=>\033[0m")

ifeq ($(origin DEBUG), undefined)
  BUILDTYPE_DIR:=release
else ifeq ($(DEBUG),0)
  BUILDTYPE_DIR:=release
else
  DOCKERFILE:=Dockerfile-debug
  BUILDTYPE_DIR:=debug
  GCFLAGS:=-gcflags="all=-N -l"
  $(info $(H) Build with debugger information)
endif

################################################################################
# Go build details                                                             #
################################################################################
BASE_PACKAGE_NAME := github.com/actionscore/actions
OUT_DIR := ./dist

HELM_CHART_DIR:=./charts/actions-operator
HELM_OUT_DIR:=$(OUT_DIR)/install
HELM_MANIFEST_FILE:=$(HELM_OUT_DIR)/actions.yaml

ACTIONS_OUT_DIR := $(OUT_DIR)/$(GOOS)_$(GOARCH)/$(BUILDTYPE_DIR)
ACTIONS_LINUX_OUT_DIR := $(OUT_DIR)/linux_$(GOARCH)/$(BUILDTYPE_DIR)
LDFLAGS := "-X $(BASE_PACKAGE_NAME)/pkg/version.commit=$(GIT_VERSION) -X $(BASE_PACKAGE_NAME)/pkg/version.version=$(ACTIONS_VERSION)"

################################################################################
# Target: build                                                                #
################################################################################
.PHONY: build
build: $(BINARIES)

# Generate builds for actions binaries for the target
# Params:
# $(1): the binary name for the target
# $(2): the output directory for the binary
# $(3): the target os
# $(4): the target arch
define genBinariesForTarget
.PHONY: $(1)
$(1):
	CGO_ENABLED=$(CGO) GOOS=$(3) GOARCH=$(4) go build $(GCFLAGS) -ldflags $(LDFLAGS) \
	-o $(OUT_DIR)/$(3)_$(4)/$(BUILDTYPE_DIR)/$(1)$(BINARY_EXT) -mod=vendor \
	$(2)/main.go;
endef

# Generate binary targets
$(foreach ITEM,$(BINARIES),$(eval $(call genBinariesForTarget,$(ITEM),./cmd/$(ITEM),$(GOOS),$(GOARCH))))

# target: build-linux
BUILD_LINUX_BINS:=$(foreach ITEM,$(BINARIES),$(ITEM)-linux)
build-linux: $(ACTIONS_LINUX_OUT_DIR) $(BUILD_LINUX_BINS)

# Generate binary targets
$(foreach ITEM,$(BINARIES),$(eval $(call genBinariesForTarget,$(ITEM)-linux,./cmd/$(ITEM),linux,$(GOARCH))))

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

LINUX_BINS_OUT_DIR=$(OUT_DIR)/linux_$(GOARCH)
DOCKER_IMAGE_TAG=$(ACTIONS_REGISTRY):$(ACTIONS_TAG)

check-docker-env:
ifeq ($(ACTIONS_REGISTRY),)
	$(error ACTIONS_REGISTRY environment variable must be set)
endif
ifeq ($(ACTIONS_TAG),)
	$(error ACTIONS_TAG environment variable must be set)
endif

docker-build: check-docker-env build-linux
	$(DOCKER) build -f $(DOCKER_DIR)/$(DOCKERFILE) $(LINUX_BINS_OUT_DIR)/. -t $(DOCKER_IMAGE_TAG)

docker-push: docker-build
	$(DOCKER) push $(DOCKER_IMAGE_TAG)

manifest-gen: actions-operator.yaml

$(HOME)/.helm:
	$(HELM) init --client-only

actions-operator.yaml: $(HOME)/.helm
	$(info Generating helm manifest $(HELM_MANIFEST_FILE)...)
	@mkdir -p $(HELM_OUT_DIR)
	$(HELM) template \
		--name=actions \
		--namespace=actions-system \
		--set-string global.tag=${ACTIONS_TAG} \
		--set-string global.registry=${ACTIONS_REGISTRY} \
		$(HELM_CHART_DIR) > $(HELM_MANIFEST_FILE)

docker-deploy-k8s: $(HOME)/.helm
	$(info Deploying ${ACTIONS_REGISTRY}:${ACTIONS_TAG} to the current context...)
	$(HELM) install \
		--name=actions \
		--namespace=actions-system \
		--set-string global.tag=${ACTIONS_TAG} \
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
