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
