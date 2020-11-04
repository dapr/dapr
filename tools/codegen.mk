ifeq (,$(shell go env GOBIN))
GOBIN=$(shell go env GOPATH)/bin
else
GOBIN=$(shell go env GOBIN)
endif

# Generate code
code-generate: controller-gen
	$(CONTROLLER_GEN) object:headerFile="./tools/boilerplate.go.txt" \
		crd:trivialVersions=true crd:crdVersions=v1 paths="./pkg/apis/..." output:crd:artifacts:config=config/crd/bases

# find or download controller-gen
# download controller-gen if necessary
controller-gen:
ifeq (, $(shell which controller-gen))
	@{ \
		set -e ;\
		CONTROLLER_GEN_TMP_DIR="$$(mktemp -d)" ;\
		cd "$$CONTROLLER_GEN_TMP_DIR" ;\
		GO111MODULE=on go get sigs.k8s.io/controller-tools/cmd/controller-gen@v0.3.0 ; \
		rm -rf "$$CONTROLLER_GEN_TMP_DIR" ;\
	}
CONTROLLER_GEN=$(GOBIN)/controller-gen
else
CONTROLLER_GEN=$(shell which controller-gen)
endif

protoc-gen-internals-v1:
	protoc -I . ./dapr/proto/internals/v1/*.proto --go_out=plugins=grpc:.
	cp -R ./github.com/dapr/dapr/pkg/proto/internals/v1/*.go ./pkg/proto/internals/v1

protoc-gen-runtime-v1:
	protoc -I . ./dapr/proto/runtime/v1/*.proto --go_out=plugins=grpc:.
	cp -R ./github.com/dapr/dapr/pkg/proto/runtime/v1/*.go ./pkg/proto/runtime/v1

# TODO(artursouza): Add auto-gen for other protos.

protoc-gen: protoc-gen-runtime-v1 protoc-gen-internals-v1