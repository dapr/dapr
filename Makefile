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

################################################################################
# Variables                                                                    #
################################################################################

export GO111MODULE ?= on
export GOPROXY ?= https://proxy.golang.org
export GOSUMDB ?= sum.golang.org

GIT_COMMIT  = $(shell git rev-list -1 HEAD)
GIT_VERSION ?= $(shell git describe --always --abbrev=7 --dirty)
# By default, disable CGO_ENABLED. See the details on https://golang.org/cmd/cgo
CGO         ?= 0
BINARIES    ?= daprd placement operator injector sentry scheduler
HA_MODE     ?= false
# Force in-memory log for placement
FORCE_INMEM ?= true
# Dapr sidecar "flavor" build tag:
# allcomponents - (default) includes all components in Dapr sidecar
# stablecomponents - includes all stable components in Dapr sidecar
DAPR_SIDECAR_FLAVOR ?= allcomponents
# Additional build tags
DAPR_GO_BUILD_TAGS ?=
ifneq ($(DAPR_GO_BUILD_TAGS),)
	DAPR_GO_BUILD_TAGS := $(DAPR_GO_BUILD_TAGS),$(DAPR_SIDECAR_FLAVOR)
else
	DAPR_GO_BUILD_TAGS := $(DAPR_SIDECAR_FLAVOR)
endif

# Add latest tag if LATEST_RELEASE is true
LATEST_RELEASE ?=

PROTOC ?=protoc

# Version of "protoc" to use
# We must also specify a protobuf "suite" version from https://github.com/protocolbuffers/protobuf/releases
PROTOC_VERSION = 25.4
PROTOBUF_SUITE_VERSION = 25.4

# name of protoc-gen-go when protoc-gen-go --version is run.
PROTOC_GEN_GO_NAME = "protoc-gen-go"
ifdef REL_VERSION
	DAPR_VERSION := $(REL_VERSION)
else
	DAPR_VERSION := edge
endif

LOCAL_ARCH := $(shell uname -m)
ifeq ($(LOCAL_ARCH),x86_64)
	TARGET_ARCH_LOCAL=amd64
else ifeq ($(shell echo $(LOCAL_ARCH) | head -c 5),armv8)
	TARGET_ARCH_LOCAL=arm64
else ifeq ($(shell echo $(LOCAL_ARCH) | head -c 4),armv)
	TARGET_ARCH_LOCAL=arm
else ifeq ($(shell echo $(LOCAL_ARCH) | head -c 5),arm64)
	TARGET_ARCH_LOCAL=arm64
else ifeq ($(shell echo $(LOCAL_ARCH) | head -c 7),aarch64)
	TARGET_ARCH_LOCAL=arm64
else
	TARGET_ARCH_LOCAL=amd64
endif
export GOARCH ?= $(TARGET_ARCH_LOCAL)

ifeq ($(GOARCH),amd64)
	LATEST_TAG?=latest
else
	LATEST_TAG?=latest-$(GOARCH)
endif

LOCAL_OS := $(shell uname)
ifeq ($(LOCAL_OS),Linux)
   TARGET_OS_LOCAL = linux
else ifeq ($(LOCAL_OS),Darwin)
   TARGET_OS_LOCAL = darwin
   PATH := $(PATH):$(HOME)/go/bin/darwin_$(GOARCH)
else
   TARGET_OS_LOCAL = windows
   PROTOC_GEN_GO_NAME := "protoc-gen-go.exe"
endif
export GOOS ?= $(TARGET_OS_LOCAL)

PROTOC_GEN_GO_VERSION = v1.32.0
PROTOC_GEN_GO_NAME+= $(PROTOC_GEN_GO_VERSION)

PROTOC_GEN_GO_GRPC_VERSION = 1.3.0

ifeq ($(TARGET_OS_LOCAL),windows)
	BUILD_TOOLS_BIN ?= build-tools.exe
	BUILD_TOOLS ?= ./.build-tools/$(BUILD_TOOLS_BIN)
	RUN_BUILD_TOOLS ?= cd .build-tools; go.exe run .
else
	BUILD_TOOLS_BIN ?= build-tools
	BUILD_TOOLS ?= ./.build-tools/$(BUILD_TOOLS_BIN)
	RUN_BUILD_TOOLS ?= cd .build-tools; GOOS=$(TARGET_OS_LOCAL) GOARCH=$(TARGET_ARCH_LOCAL) go run .
endif

# Default docker container and e2e test target.
TARGET_OS ?= linux
TARGET_ARCH ?= amd64
TEST_OUTPUT_FILE_PREFIX ?= ./test_report

GOLANGCI_LINT_TAGS=allcomponents,subtlecrypto
ifeq ($(GOOS),windows)
BINARY_EXT_LOCAL:=.exe
GOLANGCI_LINT:=golangci-lint.exe
export ARCHIVE_EXT = .zip
else
BINARY_EXT_LOCAL:=
GOLANGCI_LINT:=golangci-lint
export ARCHIVE_EXT = .tar.gz
endif

export BINARY_EXT ?= $(BINARY_EXT_LOCAL)

OUT_DIR := ./dist

# Helm template and install setting
HELM:=helm
RELEASE_NAME?=dapr
DAPR_NAMESPACE?=dapr-system
DAPR_MTLS_ENABLED?=true
DAPR_SCHEDULER_REPLICAS?=1
DAPR_SCHEDULER_IN_MEMORY_STORAGE?=false
HELM_CHART_ROOT:=./charts
HELM_CHART_DIR:=$(HELM_CHART_ROOT)/dapr
HELM_OUT_DIR:=$(OUT_DIR)/install
HELM_MANIFEST_FILE:=$(HELM_OUT_DIR)/$(RELEASE_NAME).yaml
HELM_REGISTRY?=daprio.azurecr.io


################################################################################
# Go build details                                                             #
################################################################################
BASE_PACKAGE_NAME := github.com/dapr/dapr
LOGGER_PACKAGE_NAME := github.com/dapr/kit/logger

# Comma-separated list of features to enable
ENABLED_FEATURES ?=

DEFAULT_LDFLAGS:=-X $(BASE_PACKAGE_NAME)/pkg/buildinfo.gitcommit=$(GIT_COMMIT) \
  -X $(BASE_PACKAGE_NAME)/pkg/buildinfo.gitversion=$(GIT_VERSION) \
  -X $(BASE_PACKAGE_NAME)/pkg/buildinfo.version=$(DAPR_VERSION) \
  -X $(LOGGER_PACKAGE_NAME).DaprVersion=$(DAPR_VERSION)

ifneq ($(ENABLED_FEATURES),)
  DEFAULT_LDFLAGS += -X $(BASE_PACKAGE_NAME)/pkg/buildinfo.features=$(ENABLED_FEATURES)
endif

ifeq ($(origin DEBUG), undefined)
  BUILDTYPE_DIR:=release
  LDFLAGS:="$(DEFAULT_LDFLAGS) -s -w"
else ifeq ($(DEBUG),0)
  BUILDTYPE_DIR:=release
  LDFLAGS:="$(DEFAULT_LDFLAGS) -s -w"
else
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
	CGO_ENABLED=$(CGO) GOOS=$(3) GOARCH=$(4) go build $(GCFLAGS) -ldflags=$(LDFLAGS) -tags=$(DAPR_GO_BUILD_TAGS) \
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
ARCHIVE_FILE_FLAVOR_EXTS:=$(foreach ITEM,$(BINARIES),archive-$(ITEM)-$(DAPR_SIDECAR_FLAVOR)$(ARCHIVE_EXT))

archive: $(ARCHIVE_FILE_EXTS)
archive-flavor: $(ARCHIVE_FILE_FLAVOR_EXTS)

# Generate archive files for each binary
# $(1): the binary name to be archived
# $(2): the archived file output directory
define genArchiveBinary
ifeq ($(GOOS),windows)
archive-$(1).zip:
	7z.exe a -tzip "$(2)\\$(1)_$(GOOS)_$(GOARCH)$(ARCHIVE_EXT)" "$(DAPR_OUT_DIR)\\$(1)$(BINARY_EXT)"
archive-$(1)-$(3).zip:
	7z.exe a -tzip "$(2)\\$(1)_$(GOOS)_$(GOARCH)-$(3)$(ARCHIVE_EXT)" "$(DAPR_OUT_DIR)\\$(1)$(BINARY_EXT)"
else
archive-$(1).tar.gz:
	tar czf "$(2)/$(1)_$(GOOS)_$(GOARCH)$(ARCHIVE_EXT)" -C "$(DAPR_OUT_DIR)" "$(1)$(BINARY_EXT)"
archive-$(1)-$(3).tar.gz:
	tar czf "$(2)/$(1)_$(GOOS)_$(GOARCH)-$(3)$(ARCHIVE_EXT)" -C "$(DAPR_OUT_DIR)" "$(1)$(BINARY_EXT)"
endif
endef

# Generate archive-*.[zip|tar.gz] targets
$(foreach ITEM,$(BINARIES),$(eval $(call genArchiveBinary,$(ITEM),$(ARCHIVE_OUT_DIR),$(DAPR_SIDECAR_FLAVOR))))


################################################################################
# Target: manifest-gen                                                         #
################################################################################

# Generate helm chart manifest
manifest-gen: dapr.yaml

dapr.yaml: check-docker-env
	$(info Generating helm manifest $(HELM_MANIFEST_FILE)...)
	@mkdir -p $(HELM_OUT_DIR)
	$(HELM) template \
		--include-crds=true  --set global.ha.enabled=$(HA_MODE) --set dapr_config.dapr_config_chart_included=false --set-string global.tag=$(DAPR_TAG) --set-string global.registry=$(DAPR_REGISTRY) $(HELM_CHART_DIR) > $(HELM_MANIFEST_FILE)

################################################################################
# Target: upload-helmchart													   #
################################################################################

# Upload helm charts to Helm Registry
upload-helmchart:
	export HELM_EXPERIMENTAL_OCI=1; \
	$(HELM) chart save ${HELM_CHART_ROOT}/${RELEASE_NAME} ${HELM_REGISTRY}/${HELM}/${RELEASE_NAME}:${DAPR_VERSION}; \
	$(HELM) chart push ${HELM_REGISTRY}/${HELM}/${RELEASE_NAME}:${DAPR_VERSION}

################################################################################
# Target: docker-deploy-k8s                                                    #
################################################################################

PULL_POLICY?=Always
ADDITIONAL_HELM_SET ?= ""
ifneq ($(ADDITIONAL_HELM_SET),)
	ADDITIONAL_HELM_SET := --set $(ADDITIONAL_HELM_SET)
endif
ifeq ($(DEBUG),1)
	ADDITIONAL_HELM_SET := --set dapr_operator.runAsNonRoot=false $(ADDITIONAL_HELM_SET)
	ADDITIONAL_HELM_SET := --set dapr_placement.runAsNonRoot=false $(ADDITIONAL_HELM_SET)
	ADDITIONAL_HELM_SET := --set dapr_scheduler.securityContext.runAsNonRoot=false $(ADDITIONAL_HELM_SET)
	ADDITIONAL_HELM_SET := --set dapr_scheduler.runAsNonRoot=false $(ADDITIONAL_HELM_SET)
	ADDITIONAL_HELM_SET := --set dapr_sentry.runAsNonRoot=false $(ADDITIONAL_HELM_SET)
	ADDITIONAL_HELM_SET := --set dapr_sidecar_injector.runAsNonRoot=false $(ADDITIONAL_HELM_SET)
	ADDITIONAL_HELM_SET := --set dapr_sidecar_injector.sidecarRunAsNonRoot=false $(ADDITIONAL_HELM_SET)
endif
ifeq ($(ONLY_DAPR_IMAGE),true)
	ADDITIONAL_HELM_SET := $(ADDITIONAL_HELM_SET) \
		--set dapr_operator.image.name=$(RELEASE_NAME) \
		--set dapr_placement.image.name=$(RELEASE_NAME) \
		--set dapr_sentry.image.name=$(RELEASE_NAME) \
		--set dapr_sidecar_injector.image.name=$(RELEASE_NAME) \
		--set dapr_sidecar_injector.injectorImage.name=$(RELEASE_NAME) \
		--set dapr_scheduler.image.name=$(RELEASE_NAME)
endif
docker-deploy-k8s: check-docker-env check-arch
	$(info Deploying ${DAPR_REGISTRY}/${RELEASE_NAME}:${DAPR_TAG} to the current K8S context...)
	$(HELM) upgrade --install \
		$(RELEASE_NAME) --namespace=$(DAPR_NAMESPACE) --wait --timeout 5m0s \
		--set global.ha.enabled=$(HA_MODE) --set-string global.tag=$(BUILD_TAG) \
		--set-string global.registry=$(DAPR_REGISTRY) --set global.logAsJson=true \
		--set global.daprControlPlaneOs=$(TARGET_OS) --set global.daprControlPlaneArch=$(TARGET_ARCH) \
		--set dapr_placement.logLevel=debug --set dapr_sentry.logLevel=debug \
		--set dapr_sidecar_injector.sidecarImagePullPolicy=$(PULL_POLICY) \
		--set global.imagePullPolicy=$(PULL_POLICY) --set global.imagePullSecrets=${DAPR_TEST_REGISTRY_SECRET} \
		--set global.mtls.enabled=${DAPR_MTLS_ENABLED} \
		--set dapr_placement.cluster.forceInMemoryLog=$(FORCE_INMEM) \
		--set dapr_scheduler.replicaCount=$(DAPR_SCHEDULER_REPLICAS) \
		--set dapr_scheduler.cluster.storageSize=100Mi \
		--set dapr_scheduler.cluster.inMemoryStorage=$(DAPR_SCHEDULER_IN_MEMORY_STORAGE) \
		--set dapr_scheduler.logLevel=debug \
		$(ADDITIONAL_HELM_SET) $(HELM_CHART_DIR)

################################################################################
# Target: archive                                                              #
################################################################################
release: build archive
release-flavor: build archive-flavor

################################################################################
# Target: test                                                                 #
################################################################################
.PHONY: test
test: test-deps
	CGO_ENABLED=$(CGO) \
		gotestsum \
			--jsonfile $(TEST_OUTPUT_FILE_PREFIX)_unit.json \
			--format pkgname-and-test-fails \
			-- \
				./pkg/... ./utils/... ./cmd/... \
				$(COVERAGE_OPTS) --tags=unit,allcomponents
	CGO_ENABLED=$(CGO) \
		go test --tags=allcomponents ./tests/...

################################################################################
# Target: test-race                                                            #
################################################################################
# Note that we are expliciting maintaining an allow-list of packages that should be tested
# with "-race", as many packags aren't passing those tests yet.
# Eventually, the goal is to be able to have all packages pass tests with "-race"
# Note: CGO is required for tests with "-race"
TEST_WITH_RACE=./pkg/acl/... \
./pkg/actors \
./pkg/apis/... \
./pkg/apphealth/... \
./pkg/buildinfo/... \
./pkg/channel/... \
./pkg/client/... \
./pkg/components/... \
./pkg/config/... \
./pkg/cors/... \
./pkg/diagnostics/... \
./pkg/encryption/... \
./pkg/expr/... \
./pkg/grpc/... \
./pkg/health/... \
./pkg/http/... \
./pkg/httpendpoint/... \
./pkg/injector/... \
./pkg/messages/... \
./pkg/messaging/... \
./pkg/metrics/... \
./pkg/middleware/... \
./pkg/modes/... \
./pkg/operator/... \
./pkg/outbox/... \
./pkg/placement/... \
./pkg/proto/... \
./pkg/retry/... \
./pkg/resiliency/... \
./pkg/runtime/... \
./pkg/scheduler/... \
./pkg/scopes/... \
./pkg/security/... \
./pkg/sentry/... \
./pkg/validation/... \
./utils/...

.PHONY: test-race
test-race:
	CGO_ENABLED=1 echo "$(TEST_WITH_RACE)" | xargs \
		go test -tags="allcomponents unit" -race

################################################################################
# Target: test-integration                                                     #
################################################################################

ifeq ($(GOOS),windows)
TEST_ADDITIONAL_TAGS:=,windows
else
TEST_ADDITIONAL_TAGS:=
endif

.PHONY: test-integration
test-integration: test-deps
		CGO_ENABLED=1 gotestsum \
			--jsonfile $(TEST_OUTPUT_FILE_PREFIX)_integration.json \
			--format testname \
			-- \
			./tests/integration -timeout=20m -count=1 -v -tags="integration$(TEST_ADDITIONAL_TAGS)" -integration-parallel=false

.PHONY: test-integration-parallel
test-integration-parallel: test-deps
		CGO_ENABLED=1 gotestsum \
			--jsonfile $(TEST_OUTPUT_FILE_PREFIX)_integration.json \
			--format testname \
			-- \
			./tests/integration -timeout=20m -count=1 -v -tags="integration$(TEST_ADDITIONAL_TAGS)" -integration-parallel=true

################################################################################
# Target: lint                                                                 #
################################################################################
# Please use golangci-lint version v1.64.6 , otherwise you might encounter errors.
# You can download version v1.64.6 at https://github.com/golangci/golangci-lint/releases/tag/v1.64.6
.PHONY: lint
lint: check-linter
	$(GOLANGCI_LINT) run --build-tags=$(GOLANGCI_LINT_TAGS) --timeout=20m --max-same-issues 0 --max-issues-per-linter 0

.PHONY: lint-fix
lint-fix: check-linter
	$(GOLANGCI_LINT) run --build-tags=$(GOLANGCI_LINT_TAGS) --timeout=20m --max-same-issues 0 --max-issues-per-linter 0 --fix

################################################################################
# Target: check-linter                                                         #
################################################################################
.SILENT: check-linter # Silence output other than the application run
.PHONY: check-linter
check-linter:
	$(RUN_BUILD_TOOLS) check-linter

################################################################################
# Target: modtidy-all                                                          #
################################################################################
MODFILES := $(shell find . -name go.mod)

define modtidy-target
.PHONY: modtidy-$(1)
modtidy-$(1):
	cd $(shell dirname $(1)); CGO_ENABLED=$(CGO) go mod tidy -compat=1.24.6; cd -
endef

# Generate modtidy target action for each go.mod file
$(foreach MODFILE,$(MODFILES),$(eval $(call modtidy-target,$(MODFILE))))

# Enumerate all generated modtidy targets
TIDY_MODFILES:=$(foreach ITEM,$(MODFILES),modtidy-$(ITEM))

# Define modtidy-all action trigger to run make on all generated modtidy targets
.PHONY: modtidy-all
modtidy-all: $(TIDY_MODFILES)

################################################################################
# Target: modtidy                                                              #
################################################################################
.PHONY: modtidy
modtidy:
	go mod tidy

################################################################################
# Target: format                                                               #
################################################################################
.PHONY: format
format: modtidy-all
	gofumpt -l -w . && goimports -local github.com/dapr/ -w $(shell find ./pkg -type f -name '*.go' -not -path "./pkg/proto/*")

################################################################################
# Target: check                                                                #
################################################################################
.PHONY: check
check: format test lint
	git status && [[ -z `git status -s` ]]

################################################################################
# Target: init-proto                                                           #
################################################################################
.PHONY: init-proto
init-proto:
	go install google.golang.org/protobuf/cmd/protoc-gen-go@$(PROTOC_GEN_GO_VERSION)
	go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@v$(PROTOC_GEN_GO_GRPC_VERSION)

################################################################################
# Target: gen-proto                                                            #
################################################################################
GRPC_PROTOS:=$(shell ls dapr/proto)
PROTO_PREFIX:=github.com/dapr/dapr

# Generate archive files for each binary
# $(1): the binary name to be archived
define genProtoc
.PHONY: gen-proto-$(1)
gen-proto-$(1):
	$(PROTOC) --go_out=. --go_opt=module=$(PROTO_PREFIX) --go-grpc_out=. --go-grpc_opt=require_unimplemented_servers=false,module=$(PROTO_PREFIX) ./dapr/proto/$(1)/v1/*.proto
endef

$(foreach ITEM,$(GRPC_PROTOS),$(eval $(call genProtoc,$(ITEM))))

GEN_PROTOS:=$(foreach ITEM,$(GRPC_PROTOS),gen-proto-$(ITEM))

.PHONY: gen-proto
gen-proto: check-proto-version $(GEN_PROTOS) modtidy

################################################################################
# Target: get-components-contrib                                               #
################################################################################
.PHONY: get-components-contrib
get-components-contrib:
	go get github.com/dapr/components-contrib@master
	make modtidy-all

################################################################################
# Target: check-diff                                                           #
################################################################################
.PHONY: check-diff
check-diff:
	git diff --exit-code ./go.mod # check no changes
	git diff --exit-code ./go.sum # check no changes

################################################################################
# Target: check-proto-version                                                  #
################################################################################
.PHONY: check-proto-version
check-proto-version: ## Checking the version of proto related tools
	@test "$(shell protoc --version)" = "libprotoc $(PROTOC_VERSION)" \
	|| { echo "please use protoc $(PROTOC_VERSION) (protobuf $(PROTOBUF_SUITE_VERSION)) to generate proto, see https://github.com/dapr/dapr/blob/master/dapr/README.md#proto-client-generation"; exit 1; }

	@test "$(shell protoc-gen-go-grpc --version)" = "protoc-gen-go-grpc $(PROTOC_GEN_GO_GRPC_VERSION)" \
	|| { echo "please use protoc-gen-go-grpc $(PROTOC_GEN_GO_GRPC_VERSION) to generate proto, see https://github.com/dapr/dapr/blob/master/dapr/README.md#proto-client-generation"; exit 1; }

	@test "$(shell protoc-gen-go --version 2>&1)" = "$(PROTOC_GEN_GO_NAME)" \
	|| { echo "please use protoc-gen-go $(PROTOC_GEN_GO_VERSION) to generate proto, see https://github.com/dapr/dapr/blob/master/dapr/README.md#proto-client-generation"; exit 1; }

################################################################################
# Target: check-proto-diff                                                     #
################################################################################
.PHONY: check-proto-diff
check-proto-diff:
	git diff --exit-code ./pkg/proto/common/v1/common.pb.go # check no changes
	git diff --exit-code ./pkg/proto/internals/v1/status.pb.go # check no changes
	git diff --exit-code ./pkg/proto/operator/v1/operator.pb.go # check no changes
	git diff --exit-code ./pkg/proto/operator/v1/operator_grpc.pb.go # check no changes
	git diff --exit-code ./pkg/proto/runtime/v1/appcallback.pb.go # check no changes
	git diff --exit-code ./pkg/proto/runtime/v1/appcallback_grpc.pb.go # check no changes
	git diff --exit-code ./pkg/proto/runtime/v1/dapr.pb.go # check no changes
	git diff --exit-code ./pkg/proto/runtime/v1/dapr_grpc.pb.go # check no changes
	git diff --exit-code ./pkg/proto/sentry/v1/sentry.pb.go # check no changes


################################################################################
# Target: compile-build-tools                                                  #
################################################################################
compile-build-tools:
ifeq (,$(wildcard $(BUILD_TOOLS)))
	cd .build-tools; CGO_ENABLED=$(CGO) GOOS=$(TARGET_OS_LOCAL) GOARCH=$(TARGET_ARCH_LOCAL) go build -o $(BUILD_TOOLS_BIN) .
endif

################################################################################
# Prettier                                                                     #
################################################################################
.PHONY: prettier-install prettier-check prettier-format me prettier
prettier-install:
	npm install --global prettier

prettier-check:
	npx prettier --check "*/**/*.{ts,js,mjs,json}"

prettier-format:
	npx prettier --write "*/**/*.{ts,js,mjs,json}"

# "make me prettier"
me:
	@echo "🪄💄🪄💄🪄💄"

prettier:
	@npx prettier --write "*/**/*.{ts,js,mjs,json}"

################################################################################
# Targets for components-contrib                                               #
################################################################################
.PHONY: update-components-contrib
# Branch or tag to pin
COMPONENTS_CONTRIB_BRANCH ?= master
COMPONENTS_CONTRIB_REPO ?= github.com/dapr/components-contrib
update-components-contrib:
	go get -u $(COMPONENTS_CONTRIB_REPO)@$(COMPONENTS_CONTRIB_BRANCH)
	make modtidy-all

################################################################################
# Target: codegen                                                              #
################################################################################
include tools/codegen.mk

################################################################################
# Target: docker                                                               #
################################################################################
include docker/docker.mk

################################################################################
# Target: tests                                                                #
################################################################################
include tests/dapr_tests.mk
