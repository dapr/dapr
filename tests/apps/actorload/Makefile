export GO111MODULE ?= on
export GOPROXY ?= https://proxy.golang.org
export GOSUMDB ?= sum.golang.org

BINARIES ?= stateactor testclient
OUT_DIR = ./dist

.PHONY: build
TEST_BINS:=$(foreach ITEM,$(BINARIES),$(OUT_DIR)/$(ITEM))
build: $(TEST_BINS)

define genBinariesForTarget
.PHONY: $(3)/$(1)
$(3)/$(1):
	CGO_ENABLED=0 go build -o $(3)/$(1) $(2)/main.go;
endef

# Generate binary targets
$(foreach ITEM,$(BINARIES),$(eval $(call genBinariesForTarget,$(ITEM),./cmd/$(ITEM),$(OUT_DIR))))
