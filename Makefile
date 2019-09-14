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

# Use the variable H to add a header (equivalent to =>) to informational output
H = $(shell printf "\033[34;1m=>\033[0m")

ifeq ($(origin DEBUG), undefined)
  BUILDTYPE_DIR:=release
else ifeq ($(DEBUG),0)
  BUILDTYPE_DIR:=release
else
  BUILDTYPE_DIR:=debug
  GCFLAGS:=-gcflags="all=-N -l"
  $(info $(H) Build with debugger information)
endif

################################################################################
# Go build details                                                             #
################################################################################
BASE_PACKAGE_NAME := github.com/actionscore/actions
OUT_DIR := ./dist

################################################################################
# Target: build                                                                #
################################################################################
.PHONY: build
build:
	  set -e; \
	  for b in $(BINARIES); do \
	  		for t in $(TARGETS); do \
			  	if test "windows" = $$t; then EXT=".exe"; else EXT=""; fi; \
				CGO_ENABLED=$(CGO) GOOS=$$t GOARCH=$(ARCH) go build $(GCFLAGS) \
				-ldflags "-X $(BASE_PACKAGE_NAME)/pkg/version.commit=$(GIT_VERSION) -X $(BASE_PACKAGE_NAME)/pkg/version.version=$(ACTIONS_VERSION)" \
				-o $(OUT_DIR)/"$$t"_$(ARCH)/$(BUILDTYPE_DIR)/$$b$$EXT -mod=vendor \
				cmd/$$b/main.go; \
			done; \
	  done;

################################################################################
# Target: test															       #
################################################################################
.PHONY: test
test:
	go test ./pkg/... -mod=vendor
