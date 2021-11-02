# ------------------------------------------------------------
# Copyright (c) Microsoft Corporation and Dapr Contributors.
# Licensed under the MIT License.
# ------------------------------------------------------------

# E2E test app list
# e.g. E2E_TEST_APPS=hellodapr state service_invocation
E2E_TEST_APPS=actorjava \
actordotnet \
actorpython \
actorphp \
hellodapr \
stateapp \
secretapp \
service_invocation \
service_invocation_grpc \
service_invocation_grpc_proxy_client \
service_invocation_grpc_proxy_server \
binding_input \
binding_input_grpc \
binding_output \
pubsub-publisher \
pubsub-subscriber \
pubsub-subscriber_grpc \
pubsub-subscriber-routing \
pubsub-subscriber-routing_grpc \
actorapp \
actorclientapp \
actorfeatures \
actorinvocationapp \
actorreentrancy \
runtime \
runtime_init \
middleware \
job-publisher \

# PERFORMANCE test app list
PERF_TEST_APPS=actorfeatures actorjava tester service_invocation_http

# E2E test app root directory
E2E_TESTAPP_DIR=./tests/apps

# PERFORMANCE test app root directory
PERF_TESTAPP_DIR=./tests/apps/perf

# PERFORMANCE tests
PERF_TESTS=actor_timer actor_reminder actor_activation service_invocation_http

KUBECTL=kubectl

DAPR_CONTAINER_LOG_PATH?=./dist/container_logs

DAPR_TEST_SECONDARY_NAMESPACE=dapr-tests-2

ifeq ($(DAPR_TEST_STATE_STORE),)
DAPR_TEST_STATE_STORE=redis
endif

ifeq ($(DAPR_TEST_PUBSUB),)
DAPR_TEST_PUBSUB=redis
endif

ifeq ($(DAPR_TEST_NAMESPACE),)
DAPR_TEST_NAMESPACE=$(DAPR_NAMESPACE)
endif

ifeq ($(DAPR_TEST_REGISTRY),)
DAPR_TEST_REGISTRY=$(DAPR_REGISTRY)
endif

ifeq ($(DAPR_TEST_TAG),)
DAPR_TEST_TAG=$(DAPR_TAG)-$(TARGET_OS)-$(TARGET_ARCH)
endif

ifeq ($(DAPR_TEST_ENV),minikube)
MINIKUBE_NODE_IP=$(shell minikube ip)
ifeq ($(MINIKUBE_NODE_IP),)
$(error cannot find get minikube node ip address. ensure that you have minikube environment.)
endif
endif

# check the required environment variables
check-e2e-env:
ifeq ($(DAPR_TEST_REGISTRY),)
	$(error DAPR_TEST_REGISTRY environment variable must be set)
endif
ifeq ($(DAPR_TEST_TAG),)
	$(error DAPR_TEST_TAG environment variable must be set)
endif

define genTestAppImageBuild
.PHONY: build-e2e-app-$(1)
build-e2e-app-$(1): check-e2e-env
ifeq (,$(wildcard $(E2E_TESTAPP_DIR)/$(1)/$(DOCKERFILE)))
	CGO_ENABLED=0 GOOS=$(TARGET_OS) GOARCH=$(TARGET_ARCH) go build -o $(E2E_TESTAPP_DIR)/$(1)/app$(BINARY_EXT_LOCAL) $(E2E_TESTAPP_DIR)/$(1)/app.go
	$(DOCKER) build -f $(E2E_TESTAPP_DIR)/$(DOCKERFILE) $(E2E_TESTAPP_DIR)/$(1)/. -t $(DAPR_TEST_REGISTRY)/e2e-$(1):$(DAPR_TEST_TAG)
else
	$(DOCKER) build -f $(E2E_TESTAPP_DIR)/$(1)/$(DOCKERFILE) $(E2E_TESTAPP_DIR)/$(1)/. -t $(DAPR_TEST_REGISTRY)/e2e-$(1):$(DAPR_TEST_TAG)
endif
endef

# Generate test app image build targets
$(foreach ITEM,$(E2E_TEST_APPS),$(eval $(call genTestAppImageBuild,$(ITEM))))

define genTestAppImagePush
.PHONY: push-e2e-app-$(1)
push-e2e-app-$(1): check-e2e-env
	$(DOCKER) push $(DAPR_TEST_REGISTRY)/e2e-$(1):$(DAPR_TEST_TAG)
endef

# Generate test app image push targets
$(foreach ITEM,$(E2E_TEST_APPS),$(eval $(call genTestAppImagePush,$(ITEM))))

define genTestAppImageKindPush
.PHONY: push-kind-e2e-app-$(1)
push-kind-e2e-app-$(1): check-e2e-env
	kind load docker-image $(DAPR_TEST_REGISTRY)/e2e-$(1):$(DAPR_TEST_TAG)
endef

# Generate test app image push targets
$(foreach ITEM,$(E2E_TEST_APPS),$(eval $(call genTestAppImageKindPush,$(ITEM))))

define genPerfTestAppImageBuild
.PHONY: build-perf-app-$(1)
build-perf-app-$(1): check-e2e-env
	$(DOCKER) build -f $(PERF_TESTAPP_DIR)/$(1)/$(DOCKERFILE) $(PERF_TESTAPP_DIR)/$(1)/. -t $(DAPR_TEST_REGISTRY)/perf-$(1):$(DAPR_TEST_TAG)
endef

# Generate perf app image build targets
$(foreach ITEM,$(PERF_TEST_APPS),$(eval $(call genPerfTestAppImageBuild,$(ITEM))))

define genPerfAppImagePush
.PHONY: push-perf-app-$(1)
push-perf-app-$(1): check-e2e-env
	$(DOCKER) push $(DAPR_TEST_REGISTRY)/perf-$(1):$(DAPR_TEST_TAG)
endef

define genPerfAppImageKindPush
.PHONY: push-kind-perf-app-$(1)
push-kind-perf-app-$(1): check-e2e-env
	kind load docker-image $(DAPR_TEST_REGISTRY)/perf-$(1):$(DAPR_TEST_TAG)
endef

create-test-namespace:
	kubectl create namespace $(DAPR_TEST_NAMESPACE)
	kubectl create namespace $(DAPR_TEST_SECONDARY_NAMESPACE)

delete-test-namespace:
	kubectl delete namespace $(DAPR_TEST_NAMESPACE)
	kubectl delete namespace $(DAPR_TEST_SECONDARY_NAMESPACE)

setup-3rd-party: setup-helm-init setup-test-env-redis setup-test-env-kafka

e2e-build-deploy-run: create-test-namespace setup-3rd-party build docker-push docker-deploy-k8s setup-test-components build-e2e-app-all push-e2e-app-all test-e2e-all

perf-build-deploy-run: create-test-namespace setup-3rd-party build docker-push docker-deploy-k8s setup-test-components build-perf-app-all push-perf-app-all test-perf-all

# Generate perf app image push targets
$(foreach ITEM,$(PERF_TEST_APPS),$(eval $(call genPerfAppImagePush,$(ITEM))))
# Generate perf app image kind push targets
$(foreach ITEM,$(PERF_TEST_APPS),$(eval $(call genPerfAppImageKindPush,$(ITEM))))

# Enumerate test app build targets
BUILD_E2E_APPS_TARGETS:=$(foreach ITEM,$(E2E_TEST_APPS),build-e2e-app-$(ITEM))
# Enumerate test app push targets
PUSH_E2E_APPS_TARGETS:=$(foreach ITEM,$(E2E_TEST_APPS),push-e2e-app-$(ITEM))
# Enumerate test app push targets
PUSH_KIND_E2E_APPS_TARGETS:=$(foreach ITEM,$(E2E_TEST_APPS),push-kind-e2e-app-$(ITEM))

# Enumerate test app build targets
BUILD_PERF_APPS_TARGETS:=$(foreach ITEM,$(PERF_TEST_APPS),build-perf-app-$(ITEM))
# Enumerate perf app push targets
PUSH_PERF_APPS_TARGETS:=$(foreach ITEM,$(PERF_TEST_APPS),push-perf-app-$(ITEM))
# Enumerate perf app kind push targets
PUSH_KIND_PERF_APPS_TARGETS:=$(foreach ITEM,$(PERF_TEST_APPS),push-kind-perf-app-$(ITEM))

# build test app image
build-e2e-app-all: $(BUILD_E2E_APPS_TARGETS)

# push test app image to the registry
push-e2e-app-all: $(PUSH_E2E_APPS_TARGETS)

# push test app image to kind cluster
push-kind-e2e-app-all: $(PUSH_KIND_E2E_APPS_TARGETS)

# build perf app image
build-perf-app-all: $(BUILD_PERF_APPS_TARGETS)

# push perf app image to the registry
push-perf-app-all: $(PUSH_PERF_APPS_TARGETS)

# push perf app image to kind cluster
push-kind-perf-app-all: $(PUSH_KIND_PERF_APPS_TARGETS)

.PHONY: test-deps
test-deps:
	# The desire here is to download this test dependency without polluting go.mod
	# In golang >=1.16 there is a new way to do this with `go install gotest.tools/gotestsum@latest`
	# But this doesn't work with <=1.15, so we do it the old way for now 
	# (see: https://golang.org/ref/mod#go-install)
	command -v gotestsum || GO111MODULE=off go get gotest.tools/gotestsum

# start all e2e tests
test-e2e-all: check-e2e-env test-deps
	# Note: we can set -p 2 to run two tests apps at a time, because today we do not share state between
	# tests. In the future, if we add any tests that modify global state (such as dapr config), we'll 
	# have to be sure and run them after the main test suite, so as not to alter the state of a running
	# test
	# Note2: use env variable DAPR_E2E_TEST to pick one e2e test to run.
	DAPR_CONTAINER_LOG_PATH=$(DAPR_CONTAINER_LOG_PATH) GOOS=$(TARGET_OS_LOCAL) DAPR_TEST_NAMESPACE=$(DAPR_TEST_NAMESPACE) DAPR_TEST_TAG=$(DAPR_TEST_TAG) DAPR_TEST_REGISTRY=$(DAPR_TEST_REGISTRY) DAPR_TEST_MINIKUBE_IP=$(MINIKUBE_NODE_IP) gotestsum --jsonfile $(TEST_OUTPUT_FILE_PREFIX)_e2e.json --junitfile $(TEST_OUTPUT_FILE_PREFIX)_e2e.xml --format standard-quiet -- -p 2 -count=1 -v -tags=e2e ./tests/e2e/$(DAPR_E2E_TEST)/...


define genPerfTestRun
.PHONY: test-perf-$(1)
test-perf-$(1): check-e2e-env test-deps
	DAPR_CONTAINER_LOG_PATH=$(DAPR_CONTAINER_LOG_PATH) GOOS=$(TARGET_OS_LOCAL) DAPR_TEST_NAMESPACE=$(DAPR_TEST_NAMESPACE) DAPR_TEST_TAG=$(DAPR_TEST_TAG) DAPR_TEST_REGISTRY=$(DAPR_TEST_REGISTRY) DAPR_TEST_MINIKUBE_IP=$(MINIKUBE_NODE_IP) gotestsum --jsonfile $(TEST_OUTPUT_FILE_PREFIX)_perf_$(1).json --junitfile $(TEST_OUTPUT_FILE_PREFIX)_perf_$(1).xml --format standard-quiet -- -timeout 1h -p 1 -count=1 -v -tags=perf ./tests/perf/$(1)/...
	jq -r .Output $(TEST_OUTPUT_FILE_PREFIX)_perf_$(1).json | strings
endef

# Generate perf app image build targets
$(foreach ITEM,$(PERF_TESTS),$(eval $(call genPerfTestRun,$(ITEM))))

TEST_PERF_TARGETS:=$(foreach ITEM,$(PERF_TESTS),test-perf-$(ITEM))

# start all perf tests
test-perf-all: check-e2e-env test-deps
	DAPR_CONTAINER_LOG_PATH=$(DAPR_CONTAINER_LOG_PATH) GOOS=$(TARGET_OS_LOCAL) DAPR_TEST_NAMESPACE=$(DAPR_TEST_NAMESPACE) DAPR_TEST_TAG=$(DAPR_TEST_TAG) DAPR_TEST_REGISTRY=$(DAPR_TEST_REGISTRY) DAPR_TEST_MINIKUBE_IP=$(MINIKUBE_NODE_IP) gotestsum --jsonfile $(TEST_OUTPUT_FILE_PREFIX)_perf.json --junitfile $(TEST_OUTPUT_FILE_PREFIX)_perf.xml --format standard-quiet -- -p 1 -count=1 -v -tags=perf ./tests/perf/...
	jq -r .Output $(TEST_OUTPUT_FILE_PREFIX)_perf.json | strings

# add required helm repo
setup-helm-init:
	$(HELM) repo add bitnami https://charts.bitnami.com/bitnami
	$(HELM) repo add stable https://charts.helm.sh/stable
	$(HELM) repo add incubator https://charts.helm.sh/incubator
	$(HELM) repo update

# install redis to the cluster without password
setup-test-env-redis:
	$(HELM) install dapr-redis stable/redis --wait --timeout 5m0s --namespace $(DAPR_TEST_NAMESPACE) -f ./tests/config/redis_override.yaml

delete-test-env-redis:
	${HELM} del dapr-redis --namespace ${DAPR_TEST_NAMESPACE}

# install kafka to the cluster
setup-test-env-kafka:
	$(HELM) install dapr-kafka bitnami/kafka -f ./tests/config/kafka_override.yaml --namespace $(DAPR_TEST_NAMESPACE) --timeout 10m0s

# delete kafka from cluster
delete-test-env-kafka:
	$(HELM) del dapr-kafka --namespace $(DAPR_TEST_NAMESPACE) 

# Install redis and kafka to test cluster
setup-test-env: setup-test-env-kafka setup-test-env-redis

save-dapr-control-plane-k8s-logs:
	mkdir -p '$(DAPR_CONTAINER_LOG_PATH)'
	kubectl logs -l 'app.kubernetes.io/name=dapr' -n $(DAPR_TEST_NAMESPACE) > '$(DAPR_CONTAINER_LOG_PATH)/control_plane_containers.log'

# Apply default config yaml to turn mTLS off for testing (mTLS is enabled by default)
setup-disable-mtls:
	$(KUBECTL) apply -f ./tests/config/dapr_mtls_off_config.yaml --namespace $(DAPR_TEST_NAMESPACE)

# Apply default config yaml to turn tracing off for testing (tracing is enabled by default)
setup-app-configurations:
	$(KUBECTL) apply -f ./tests/config/dapr_observability_test_config.yaml --namespace $(DAPR_TEST_NAMESPACE)

# Apply component yaml for state, secrets, pubsub, and bindings
setup-test-components: setup-app-configurations
	$(KUBECTL) apply -f ./tests/config/kubernetes_secret.yaml --namespace $(DAPR_TEST_NAMESPACE)
	$(KUBECTL) apply -f ./tests/config/kubernetes_secret_config.yaml --namespace $(DAPR_TEST_NAMESPACE)
	$(KUBECTL) apply -f ./tests/config/kubernetes_redis_secret.yaml --namespace $(DAPR_TEST_NAMESPACE)
	$(KUBECTL) apply -f ./tests/config/dapr_$(DAPR_TEST_STATE_STORE)_state.yaml --namespace $(DAPR_TEST_NAMESPACE)
	$(KUBECTL) apply -f ./tests/config/dapr_tests_cluster_role_binding.yaml --namespace $(DAPR_TEST_NAMESPACE)
	$(KUBECTL) apply -f ./tests/config/dapr_$(DAPR_TEST_PUBSUB)_pubsub.yaml --namespace $(DAPR_TEST_NAMESPACE)
	$(KUBECTL) apply -f ./tests/config/dapr_kafka_bindings.yaml --namespace $(DAPR_TEST_NAMESPACE)
	$(KUBECTL) apply -f ./tests/config/dapr_kafka_bindings_custom_route.yaml --namespace $(DAPR_TEST_NAMESPACE)
	$(KUBECTL) apply -f ./tests/config/dapr_kafka_bindings_grpc.yaml --namespace $(DAPR_TEST_NAMESPACE)
	$(KUBECTL) apply -f ./tests/config/app_topic_subscription_pubsub.yaml --namespace $(DAPR_TEST_NAMESPACE)
	$(KUBECTL) apply -f ./tests/config/app_topic_subscription_pubsub_grpc.yaml --namespace $(DAPR_TEST_NAMESPACE)
	$(KUBECTL) apply -f ./tests/config/kubernetes_allowlists_config.yaml --namespace $(DAPR_TEST_NAMESPACE)
	$(KUBECTL) apply -f ./tests/config/kubernetes_allowlists_grpc_config.yaml --namespace $(DAPR_TEST_NAMESPACE)
	$(KUBECTL) apply -f ./tests/config/dapr_redis_state_badhost.yaml --namespace $(DAPR_TEST_NAMESPACE)
	$(KUBECTL) apply -f ./tests/config/dapr_redis_state_badpass.yaml --namespace $(DAPR_TEST_NAMESPACE)
	$(KUBECTL) apply -f ./tests/config/uppercase.yaml --namespace $(DAPR_TEST_NAMESPACE)
	$(KUBECTL) apply -f ./tests/config/pipeline.yaml --namespace $(DAPR_TEST_NAMESPACE)
	$(KUBECTL) apply -f ./tests/config/app_reentrant_actor.yaml --namespace $(DAPR_TEST_NAMESPACE)
	$(KUBECTL) apply -f ./tests/config/app_actor_type_metadata.yaml --namespace $(DAPR_TEST_NAMESPACE)
	$(KUBECTL) apply -f ./tests/config/app_topic_subscription_routing.yaml --namespace $(DAPR_TEST_NAMESPACE)
	$(KUBECTL) apply -f ./tests/config/app_topic_subscription_routing_grpc.yaml --namespace $(DAPR_TEST_NAMESPACE)
	$(KUBECTL) apply -f ./tests/config/app_pubsub_routing.yaml --namespace $(DAPR_TEST_NAMESPACE)
	$(KUBECTL) apply -f ./tests/config/kubernetes_grpc_proxy_config.yaml --namespace $(DAPR_TEST_NAMESPACE)

	# Show the installed components
	$(KUBECTL) get components --namespace $(DAPR_TEST_NAMESPACE)

	# Show the installed configurations
	$(KUBECTL) get configurations --namespace $(DAPR_TEST_NAMESPACE)

# Clean up test environment
clean-test-env:
	./tests/test-infra/clean_up.sh $(DAPR_TEST_NAMESPACE)
	./tests/test-infra/clean_up.sh $(DAPR_TEST_NAMESPACE)-2

# Setup kind
setup-kind:
	kind create cluster --config ./tests/config/kind.yaml --name kind
	kubectl cluster-info --context kind-kind
	# Setup registry
	docker run -d --restart=always -p 5000:5000 --name kind-registry registry:2
	# Connect the registry to the KinD network.
	docker network connect "kind" kind-registry
	# Setup metrics-server
	helm install ms stable/metrics-server -n kube-system --set=args={--kubelet-insecure-tls}

describe-kind-env:
	@echo "\
	export MINIKUBE_NODE_IP=`kubectl get nodes \
	    -lkubernetes.io/hostname!=kind-control-plane \
        -ojsonpath='{.items[0].status.addresses[?(@.type=="InternalIP")].address}'`\n\
	export DAPR_REGISTRY=$${DAPR_REGISTRY:-localhost:5000/dapr}\n\
	export DAPR_TEST_REGISTRY=$${DAPR_TEST_REGISTRY:-localhost:5000/dapr}\n\
	export DAPR_TAG=dev\n\
	export DAPR_NAMESPACE=dapr-tests"
	

delete-kind:
	docker stop kind-registry && docker rm kind-registry || echo "Could not delete registry."
	kind delete cluster --name kind

ifeq ($(OS),Windows_NT) 
    detected_OS := windows
else
    detected_OS := $(shell sh -c 'uname 2>/dev/null || echo Unknown' |  tr '[:upper:]' '[:lower:]')
endif

setup-minikube-darwin:
	minikube start --memory=4g --cpus=4 --driver=hyperkit --kubernetes-version=v1.18.8
	minikube addons enable metrics-server

setup-minikube-windows:
	minikube start --memory=4g --cpus=4 --kubernetes-version=v1.18.8
	minikube addons enable metrics-server

setup-minikube-linux:
	minikube start --memory=4g --cpus=4 --kubernetes-version=v1.18.8
	minikube addons enable metrics-server

setup-minikube: setup-minikube-$(detected_OS)

describe-minikube-env:
	@echo "\
	export DAPR_REGISTRY=docker.io/`docker-credential-desktop list | jq -r '\
	. | to_entries[] | select(.key | contains("docker.io")) | last(.value)'`\n\
	export DAPR_TAG=dev\n\
	export DAPR_NAMESPACE=dapr-tests\n\
	export DAPR_TEST_ENV=minikube\n\
	export DAPR_TEST_REGISTRY=\n\
	export MINIKUBE_NODE_IP="

# Setup minikube
delete-minikube:
	minikube delete
