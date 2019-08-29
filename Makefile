################################################################################
# Variables																       #
################################################################################

export GO111MODULE=on

GIT_COMMIT  = $(shell git rev-list -1 HEAD)
GIT_VERSION = $(shell git describe --always --abbrev=7 --dirty)
TARGETS		?= darwin linux windows
ARCH		?= amd64
CGO			?= 0
BINARIES    ?= actionsrt placement operator injector

ifdef REL_VERSION
	ACTIONS_VERSION := $(REL_VERSION)
else
	ACTIONS_VERSION := edge
endif

################################################################################
# Go build details                                                             #
################################################################################

BASE_PACKAGE_NAME := github.com/actionscore/actions

################################################################################
# Dependencies                                                                 #
################################################################################

.PHONY: dep
dep:
	go mod vendor

################################################################################
# Build																           #
################################################################################

.PHONY: build
build: dep
	  set -e; \
	  for b in $(BINARIES); do \
	  		for t in $(TARGETS); do \
			  	if test "windows" = $$t; then EXT=".exe"; else EXT=""; fi; \
				CGO_ENABLED=$(CGO) GOOS=$$t GOARCH=$(ARCH) go build \
				-ldflags "-X $(BASE_PACKAGE_NAME)/pkg/version.commit=$(GIT_VERSION) -X $(BASE_PACKAGE_NAME)/pkg/version.version=$(ACTIONS_VERSION)" \
				-o dist/"$$t"_$(ARCH)/$$b$$EXT -mod=vendor \
				cmd/$$b/main.go; \
			done; \
	  done;

################################################################################
# Tests																           #
################################################################################
.PHONY: test
test: dep
	go test ./pkg/... -mod=readonly
