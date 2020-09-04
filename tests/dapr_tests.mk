# ------------------------------------------------------------
# Copyright (c) Microsoft Corporation.
# Licensed under the MIT License.
# ------------------------------------------------------------

# E2E test app list
# e.g. E2E_TEST_APPS=hellodapr state service_invocation
E2E_TEST_APPS=hellodapr stateapp secretapp service_invocation service_invocation_grpc binding_input binding_output pubsub-publisher pubsub-subscriber actorapp actorfeatures runtime runtime_init

# PERFORMACE test app list
PERF_TEST_APPS=tester service_invocation_http

# E2E test app root directory
E2E_TESTAPP_DIR=./tests/apps

# PERFORMANCE test app root directory
PERF_TESTAPP_DIR=./tests/apps/perf

KUBECTL=kubectl

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
	GOOS=$(TARGET_OS) GOARCH=$(TARGET_ARCH) go build -o $(E2E_TESTAPP_DIR)/$(1)/app$(BINARY_EXT_LOCAL) $(E2E_TESTAPP_DIR)/$(1)/app.go
	$(DOCKER) build -f $(E2E_TESTAPP_DIR)/$(DOCKERFILE) $(E2E_TESTAPP_DIR)/$(1)/. -t $(DAPR_TEST_REGISTRY)/e2e-$(1):$(DAPR_TEST_TAG)
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

# Generate perf app image push targets
$(foreach ITEM,$(PERF_TEST_APPS),$(eval $(call genPerfAppImagePush,$(ITEM))))

# Enumerate test app build targets
BUILD_E2E_APPS_TARGETS:=$(foreach ITEM,$(E2E_TEST_APPS),build-e2e-app-$(ITEM))
# Enumerate test app push targets
PUSH_E2E_APPS_TARGETS:=$(foreach ITEM,$(E2E_TEST_APPS),push-e2e-app-$(ITEM))

# Enumerate test app build targets
BUILD_PERF_APPS_TARGETS:=$(foreach ITEM,$(PERF_TEST_APPS),build-perf-app-$(ITEM))
# Enumerate test app push targets
PUSH_PERF_APPS_TARGETS:=$(foreach ITEM,$(PERF_TEST_APPS),push-perf-app-$(ITEM))

# build test app image
build-e2e-app-all: $(BUILD_E2E_APPS_TARGETS)

# push test app image to the registry
push-e2e-app-all: $(PUSH_E2E_APPS_TARGETS)

# build perf app image
build-perf-app-all: $(BUILD_PERF_APPS_TARGETS)

# push perf app image to the registry
push-perf-app-all: $(PUSH_PERF_APPS_TARGETS)

# start all e2e tests
test-e2e-all: check-e2e-env
	# Note: we can set -p 2 to run two tests apps at a time, because today we do not share state between
	# tests. In the future, if we add any tests that modify global state (such as dapr config), we'll 
	# have to be sure and run them after the main test suite, so as not to alter the state of a running
	# test
	DAPR_TEST_NAMESPACE=$(DAPR_TEST_NAMESPACE) DAPR_TEST_TAG=$(DAPR_TEST_TAG) DAPR_TEST_REGISTRY=$(DAPR_TEST_REGISTRY) DAPR_TEST_MINIKUBE_IP=$(MINIKUBE_NODE_IP) go test -p 2 -count=1 -v -tags=e2e ./tests/e2e/...

# start all perf tests
test-perf-all: check-e2e-env
	DAPR_TEST_NAMESPACE=$(DAPR_TEST_NAMESPACE) DAPR_TEST_TAG=$(DAPR_TEST_TAG) DAPR_TEST_REGISTRY=$(DAPR_TEST_REGISTRY) DAPR_TEST_MINIKUBE_IP=$(MINIKUBE_NODE_IP) go test -p 1 -count=1 -v -tags=perf ./tests/perf/...

# add required helm repo
setup-helm-init:
	$(HELM) repo add stable https://kubernetes-charts.storage.googleapis.com/
	$(HELM) repo add incubator http://storage.googleapis.com/kubernetes-charts-incubator
	$(HELM) repo update

# install redis to the cluster without password
setup-test-env-redis:
	$(HELM) install dapr-redis stable/redis --wait --timeout 5m0s --namespace $(DAPR_TEST_NAMESPACE) -f ./tests/config/redis_override.yaml

# install kafka to the cluster
setup-test-env-kafka:
	$(HELM) template dapr-kafka incubator/kafka --wait --timeout 10m0s -f ./tests/config/kafka_override.yaml | python ./tests/config/modify_kafka_template.py | kubectl apply -f - --namespace $(DAPR_TEST_NAMESPACE)

# Install redis and kafka to test cluster
setup-test-env: setup-test-env-kafka setup-test-env-redis

# Apply default config yaml to turn mTLS off for testing (mTLS is enabled by default)
setup-disable-mtls:
	$(KUBECTL) apply -f ./tests/config/dapr_mtls_off_config.yaml --namespace $(DAPR_TEST_NAMESPACE)

# Apply default config yaml to turn tracing off for testing (tracing is enabled by default)
setup-app-configurations:
	$(KUBECTL) apply -f ./tests/config/dapr_telemetry_off_config.yaml --namespace $(DAPR_TEST_NAMESPACE)

# Apply component yaml for state, secrets, pubsub, and bindings
setup-test-components:
	$(KUBECTL) apply -f ./tests/config/kubernetes_secret.yaml --namespace $(DAPR_TEST_NAMESPACE)
	$(KUBECTL) apply -f ./tests/config/kubernetes_redis_secret.yaml --namespace $(DAPR_TEST_NAMESPACE)
	$(KUBECTL) apply -f ./tests/config/dapr_redis_state.yaml --namespace $(DAPR_TEST_NAMESPACE)
	$(KUBECTL) apply -f ./tests/config/dapr_tests_cluster_role_binding.yaml --namespace $(DAPR_TEST_NAMESPACE)
	$(KUBECTL) apply -f ./tests/config/dapr_redis_pubsub.yaml --namespace $(DAPR_TEST_NAMESPACE)
	$(KUBECTL) apply -f ./tests/config/dapr_kafka_bindings.yaml --namespace $(DAPR_TEST_NAMESPACE)

	# Show the installed components
	$(KUBECTL) get components --namespace $(DAPR_TEST_NAMESPACE)

# Clean up test environment
clean-test-env:
	./tests/test-infra/clean_up.sh $(DAPR_TEST_NAMESPACE)
