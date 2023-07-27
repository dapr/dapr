PROTOS = operator placement sentry common runtime internals components

ifeq (,$(shell go env GOBIN))
GOBIN=$(shell go env GOPATH)/bin
else
GOBIN=$(shell go env GOBIN)
endif

# Generate code
code-generate: controller-gen
	$(CONTROLLER_GEN) object:headerFile="./tools/boilerplate.go.txt" \
		crd:crdVersions=v1 paths="./pkg/apis/..." output:crd:artifacts:config=config/crd/bases
	$(CONTROLLER_GEN) object:headerFile="./tools/boilerplate.go.txt" \
		paths="./pkg/apis/..." 

# find or download controller-gen
# download controller-gen if necessary
controller-gen:
ifeq (, $(shell which controller-gen))
	@{ \
		set -e ;\
		CONTROLLER_GEN_TMP_DIR="$$(mktemp -d)" ;\
		cd "$$CONTROLLER_GEN_TMP_DIR" ;\
		go install sigs.k8s.io/controller-tools/cmd/controller-gen@v0.11.3 ; \
		rm -rf "$$CONTROLLER_GEN_TMP_DIR" ;\
	}
CONTROLLER_GEN=$(GOBIN)/controller-gen
else
CONTROLLER_GEN=$(shell which controller-gen)
endif


define genProtoForTarget
.PHONY: $(1)
protoc-gen-$(1)-v1:
	protoc -I . ./dapr/proto/$(1)/v1/*.proto --go_out=plugins=grpc:.
	cp -R ./github.com/dapr/dapr/pkg/proto/$(1)/v1/*.go ./pkg/proto/$(1)/v1
	rm -rf ./github.com
endef

# Generate proto gen targets
$(foreach ITEM,$(PROTOS),$(eval $(call genProtoForTarget,$(ITEM))))

PROTOC_ALL_TARGETS:=$(foreach ITEM,$(PROTOS),protoc-gen-$(ITEM)-v1)

protoc-gen: $(PROTOC_ALL_TARGETS)