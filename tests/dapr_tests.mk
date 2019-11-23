# ------------------------------------------------------------
# Copyright (c) Microsoft Corporation.
# Licensed under the MIT License.
# ------------------------------------------------------------

# E2E test app list
# e.g. E2E_TEST_APPS=hellodapr state serviceinvocation
E2E_TEST_APPS=hellodapr

# E2E test app root directory
E2E_TESTAPP_DIR=./tests/apps

KUBECTL=kubectl
DAPR_TEST_NAMESPACE?=dapr-test

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
	DAPR_TEST_MINIKUBE_IP=$(MINIKUBE_NODE_IP) go test -v -tags=e2e ./tests/e2e/...

setup-helm-init:
	$(HELM) repo add stable https://kubernetes-charts.storage.googleapis.com/
	$(HELM) repo add incubator http://storage.googleapis.com/kubernetes-charts-incubator
	$(HELM) repo update

setup-test-env-redis:
	# install redis to the cluster without password
	$(HELM) install --wait --name dapr-redis --set usePassword=false stable/redis --namespace $(DAPR_TEST_NAMESPACE)

setup-test-env-kafka:
	# install kafka to the cluster
	$(HELM) install -f ./config/kafka_override.yaml --wait --name dapr-kafka --namespace $(DAPR_TEST_NAMESPACE) incubator/kafka

setup-test-env: setup-test-env-redis setup-test-env-kafka
	# Apply component yaml for state, pubsub, and bindings
	$(KUBECTL) apply -f ./config/dapr_redis_state.yaml --namespace $(DAPR_TEST_NAMESPACE)
	$(KUBECTL) apply -f ./config/dapr_redis_pubsub.yaml --namespace $(DAPR_TEST_NAMESPACE)
	$(KUBECTL) apply -f ./config/dapr_kafka_bindings.yaml --namespace $(DAPR_TEST_NAMESPACE)

	# Show the installed components
	$(KUBECTL) get components --namespace $(DAPR_TEST_NAMESPACE)

clean-test-env:
	$(HELM) delete --purge dapr-redis
	$(HELM) delete --purge dapr-kafka
	$(HELM) delete --purge dapr
	$(KUBECTL) -n $(DAPR_TEST_NAMESPACE) delete pod,deployment,svc --all