# ------------------------------------------------------------
# Copyright (c) Microsoft Corporation.
# Licensed under the MIT License.
# ------------------------------------------------------------

# E2E test app list
# e.g. E2E_TEST_APPS=hellodapr state service_invocation
E2E_TEST_APPS=hellodapr stateapp service_invocation binding_input binding_output pubsub-publisher pubsub-subscriber actorapp

# E2E test app root directory
E2E_TESTAPP_DIR=./tests/apps

KUBECTL=kubectl

ifeq ($(DAPR_TEST_NAMESPACE),)
DAPR_TEST_NAMESPACE=$(HELM_NAMESPACE)
endif

ifeq ($(DAPR_TEST_REGISTRY),)
DAPR_TEST_REGISTRY=$(DAPR_REGISTRY)
endif

ifeq ($(DAPR_TEST_TAG),)
DAPR_TEST_TAG=$(DAPR_TAG)
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
	$(DOCKER) build -f $(E2E_TESTAPP_DIR)/$(1)/$(DOCKERFILE) $(E2E_TESTAPP_DIR)/$(1)/. -t $(DAPR_TEST_REGISTRY)/e2e-$(1):$(DAPR_TEST_TAG)
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

# Enumerate test app build targets
BUILD_E2E_APPS_TARGETS:=$(foreach ITEM,$(E2E_TEST_APPS),build-e2e-app-$(ITEM))
# Enumerate test app push targets
PUSH_E2E_APPS_TARGETS:=$(foreach ITEM,$(E2E_TEST_APPS),push-e2e-app-$(ITEM))

# build test app image
build-e2e-app-all: $(BUILD_E2E_APPS_TARGETS)

# push test app image to the registry
push-e2e-app-all: $(PUSH_E2E_APPS_TARGETS)

# start all e2e tests
test-e2e-all: check-e2e-env
	DAPR_TEST_NAMESPACE=$(DAPR_TEST_NAMESPACE) DAPR_TEST_TAG=$(DAPR_TEST_TAG) DAPR_TEST_REGISTRY=$(DAPR_TEST_REGISTRY) DAPR_TEST_MINIKUBE_IP=$(MINIKUBE_NODE_IP) go test -v -tags=e2e ./tests/e2e/...

# add required helm repo
setup-helm-init: $(HOME)/.helm
	$(HELM) repo add stable https://kubernetes-charts.storage.googleapis.com/
	$(HELM) repo add incubator http://storage.googleapis.com/kubernetes-charts-incubator
	$(HELM) repo update

# install redis to the cluster without password
setup-test-env-redis:
	$(HELM) install --wait --timeout 1000 --name dapr-redis --set usePassword=false stable/redis --namespace $(DAPR_TEST_NAMESPACE)

# install kafka to the cluster
setup-test-env-kafka:
	$(HELM) install -f ./tests/config/kafka_override.yaml --wait --timeout 1000 --name dapr-kafka --namespace $(DAPR_TEST_NAMESPACE) incubator/kafka

# Install redis and kafka to test cluster
setup-test-env: setup-test-env-redis setup-test-env-kafka

# Apply component yaml for state, pubsub, and bindings
setup-test-components:
	$(KUBECTL) apply -f ./tests/config/dapr_redis_state.yaml --namespace $(DAPR_TEST_NAMESPACE)
	$(KUBECTL) apply -f ./tests/config/dapr_redis_pubsub.yaml --namespace $(DAPR_TEST_NAMESPACE)
	$(KUBECTL) apply -f ./tests/config/dapr_kafka_bindings.yaml --namespace $(DAPR_TEST_NAMESPACE)

	# Show the installed components
	$(KUBECTL) get components --namespace $(DAPR_TEST_NAMESPACE)

# Clean up test environment
clean-test-env: $(HOME)/.helm
	./tests/test-infra/clean_up.sh $(DAPR_TEST_NAMESPACE)
