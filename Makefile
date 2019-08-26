################################################################################
# Variables																       #
################################################################################

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
# Dependencies																   #
################################################################################

.PHONY: dep
dep:
ifeq ($(shell command -v dep 2> /dev/null),)
	go get -u -v github.com/golang/dep/cmd/dep
endif

.PHONY: deps
deps: dep
	dep ensure -v

################################################################################
# Build																           #
################################################################################

.PHONY: build
build:
	  set -e; \
	  for b in $(BINARIES); do \
	  		for t in $(TARGETS); do \
			  	if test "windows" = $$t; then EXT=".exe"; else EXT=""; fi; \
				CGO_ENABLED=$(CGO) GOOS=$$t GOARCH=$(ARCH) go build \
				-ldflags "-X $(BASE_PACKAGE_NAME)/pkg/version.commit=$(GIT_VERSION) -X $(BASE_PACKAGE_NAME)/pkg/version.version=$(ACTIONS_VERSION)" \
				-o dist/"$$t"_$(ARCH)/$$b$$EXT \
				cmd/$$b/main.go; \
			done; \
	  done;

################################################################################
# Tests																           #
################################################################################
.PHONY: test
test:
	go test ./pkg/...
