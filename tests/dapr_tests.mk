#
# Copyright 2021 The Dapr Authors
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#     http://www.apache.org/licenses/LICENSE-2.0
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.
#

# E2E test app list (folder name)
# e.g. E2E_TEST_APPS=hellodapr state service_invocation
E2E_TEST_APPS=actorjava \
actordotnet \
actorpython \
actorphp \
healthapp \
hellodapr \
schedulerapp \
schedulerapp_grpc \
stateapp \
secretapp \
service_invocation \
service_invocation_external \
service_invocation_grpc \
service_invocation_grpc_proxy_client \
service_invocation_grpc_proxy_server \
binding_input \
binding_input_grpc \
binding_output \
pubsub-publisher \
pubsub-subscriber \
pubsub-bulk-subscriber \
pubsub-subscriber_grpc \
pubsub-subscriber-routing \
pubsub-subscriber-routing_grpc \
pubsub-subscriber-streaming \
pubsub-publisher-streaming \
actorapp \
actorclientapp \
actorfeatures \
actorinvocationapp \
actorstate \
actorreentrancy \
crypto \
runtime \
runtime_init \
middleware \
job-publisher \
resiliencyapp \
resiliencyapp_grpc \
injectorapp \
injectorapp-init \
metadata \
pluggable_redis-statestore \
pluggable_redis-pubsub \
pluggable_kafka-bindings \
tracingapp \
configurationapp \
workflowsapp \

# PERFORMANCE test app list
PERF_TEST_APPS=actorfeatures actorjava tester service_invocation_http service_invocation_grpc actor-activation-locker k6-custom pubsub_subscribe_http configuration workflowsapp

# E2E test app root directory
E2E_TESTAPP_DIR=./tests/apps

# PERFORMANCE test app root directory
PERF_TESTAPP_DIR=./tests/apps/perf

# PERFORMANCE tests
PERF_TESTS=actor_activation \
actor_double_activation \
actor_id_scale \
actor_reminder \
actor_timer \
actor_type_scale \
configuration \
pubsub_bulk_publish_grpc \
pubsub_bulk_publish_http \
pubsub_publish_grpc \
pubsub_publish_http \
pubsub_subscribe_http \
scheduler \
service_invocation_grpc \
service_invocation_http \
state_get_grpc \
state_get_http \
workflows \

KUBECTL=kubectl

DAPR_CONTAINER_LOG_PATH?=./dist/container_logs
DAPR_TEST_LOG_PATH?=./dist/logs

ifeq ($(DAPR_TEST_STATE_STORE),)
DAPR_TEST_STATE_STORE=postgres
endif

ifeq ($(DAPR_TEST_QUERY_STATE_STORE),)
DAPR_TEST_QUERY_STATE_STORE=postgres
endif

ifeq ($(DAPR_TEST_PUBSUB),)
DAPR_TEST_PUBSUB=redis
endif

ifeq ($(DAPR_TEST_CONFIG_STORE),)
DAPR_TEST_CONFIG_STORE=redis
endif

ifeq ($(DAPR_TEST_CRYPTO),)
DAPR_TEST_CRYPTO=jwks
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

ifeq ($(DAPR_TEST_KIND_CLUSTER_NAME),)
DAPR_TEST_KIND_CLUSTER_NAME=kind
endif

ifeq ($(DAPR_TEST_REGISTRY_PORT),)
DAPR_TEST_REGISTRY_PORT=5000
endif


ifeq ($(DAPR_PERF_PUBSUB_SUBS_HTTP_TEST_CONFIG_FILE_NAME),)
DAPR_PERF_PUBSUB_SUBS_HTTP_TEST_CONFIG_FILE_NAME=test_all.yaml
endif

ifeq ($(WINDOWS_VERSION),)
WINDOWS_VERSION=ltsc2022
endif

# check the required environment variables
check-e2e-env:
ifeq ($(DAPR_TEST_REGISTRY),)
	$(error DAPR_TEST_REGISTRY environment variable must be set)
endif
ifeq ($(DAPR_TEST_TAG),)
	$(error DAPR_TEST_TAG environment variable must be set)
endif

check-e2e-cache:
ifeq ($(DAPR_CACHE_REGISTRY),)
	$(error DAPR_CACHE_REGISTRY environment variable must be set)
endif

define genTestAppImageBuild
.PHONY: build-e2e-app-$(1)
build-e2e-app-$(1): check-e2e-env
	$(RUN_BUILD_TOOLS) e2e build \
		--name "$(1)" \
		--appdir "../$(E2E_TESTAPP_DIR)" \
		--dest-registry "$(DAPR_TEST_REGISTRY)" \
		--dest-tag "$(DAPR_TEST_TAG)" \
		--dockerfile "$(DOCKERFILE)" \
		--target-os "$(TARGET_OS)" \
		--target-arch "$(TARGET_ARCH)" \
		--cache-registry "$(DAPR_CACHE_REGISTRY)"
endef

# Generate test app image build targets
$(foreach ITEM,$(E2E_TEST_APPS),$(eval $(call genTestAppImageBuild,$(ITEM))))

define genTestAppImagePush
.PHONY: push-e2e-app-$(1)
push-e2e-app-$(1): check-e2e-env
	$(RUN_BUILD_TOOLS) e2e push \
		--name "$(1)" \
		--dest-registry "$(DAPR_TEST_REGISTRY)" \
		--dest-tag "$(DAPR_TEST_TAG)"
endef

# Generate test app image push targets
$(foreach ITEM,$(E2E_TEST_APPS),$(eval $(call genTestAppImagePush,$(ITEM))))

define genTestAppImageBuildPush
.PHONY: build-push-e2e-app-$(1)
build-push-e2e-app-$(1): check-e2e-env check-e2e-cache
	$(RUN_BUILD_TOOLS) e2e build-and-push \
		--name "$(1)" \
		--appdir "../$(E2E_TESTAPP_DIR)" \
		--dest-registry "$(DAPR_TEST_REGISTRY)" \
		--dest-tag "$(DAPR_TEST_TAG)" \
		--dockerfile "$(DOCKERFILE)" \
		--target-os "$(TARGET_OS)" \
		--target-arch "$(TARGET_ARCH)" \
		--cache-registry "$(DAPR_CACHE_REGISTRY)" \
		--windows-version "$(WINDOWS_VERSION)"
endef

# Generate test app image build-push targets
$(foreach ITEM,$(E2E_TEST_APPS),$(eval $(call genTestAppImageBuildPush,$(ITEM))))

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
	$(RUN_BUILD_TOOLS) perf build \
		--name "$(1)" \
		--appdir "../$(E2E_TESTAPP_DIR)" \
		--dest-registry "$(DAPR_TEST_REGISTRY)" \
		--dest-tag "$(DAPR_TEST_TAG)" \
		--target-os "$(TARGET_OS)" \
		--target-arch "$(TARGET_ARCH)" \
		--cache-registry "$(DAPR_CACHE_REGISTRY)"
endef

# Generate perf app image build targets
$(foreach ITEM,$(PERF_TEST_APPS),$(eval $(call genPerfTestAppImageBuild,$(ITEM))))

define genPerfAppImagePush
.PHONY: push-perf-app-$(1)
push-perf-app-$(1): check-e2e-env
	$(RUN_BUILD_TOOLS) perf push \
		--name "$(1)" \
		--dest-registry "$(DAPR_TEST_REGISTRY)" \
		--dest-tag "$(DAPR_TEST_TAG)"
endef

define genPerfAppImageBuildPush
.PHONY: build-push-perf-app-$(1)
build-push-perf-app-$(1): check-e2e-env check-e2e-cache
	$(RUN_BUILD_TOOLS) perf build-and-push \
		--name "$(1)" \
		--appdir "../$(E2E_TESTAPP_DIR)" \
		--dest-registry "$(DAPR_TEST_REGISTRY)" \
		--dest-tag "$(DAPR_TEST_TAG)" \
		--cache-registry "$(DAPR_CACHE_REGISTRY)"
endef

# Generate perf app image build-push targets
$(foreach ITEM,$(PERF_TEST_APPS),$(eval $(call genPerfAppImageBuildPush,$(ITEM))))

define genPerfAppImageKindPush
.PHONY: push-kind-perf-app-$(1)
push-kind-perf-app-$(1): check-e2e-env
	kind load docker-image $(DAPR_TEST_REGISTRY)/perf-$(1):$(DAPR_TEST_TAG)
endef

create-test-namespace:
	kubectl create namespace $(DAPR_TEST_NAMESPACE)

delete-test-namespace:
	kubectl delete namespace $(DAPR_TEST_NAMESPACE) aa

setup-3rd-party: setup-helm-init setup-test-env-redis setup-test-env-kafka setup-test-env-zipkin setup-test-env-postgres

setup-pubsub-subs-perf-test-components: setup-test-env-rabbitmq setup-test-env-pulsar setup-test-env-mqtt

build-deploy: build docker-push docker-deploy-k8s

init-build-deploy: create-test-namespace setup-3rd-party build-deploy

e2e-build-deploy-run: init-build-deploy setup-test-components build-e2e-app-all push-e2e-app-all test-e2e-all

perf-build-deploy-run: init-build-deploy setup-test-components build-perf-app-all push-perf-app-all test-perf-all

# Generate perf app image push targets
$(foreach ITEM,$(PERF_TEST_APPS),$(eval $(call genPerfAppImagePush,$(ITEM))))
# Generate perf app image kind push targets
$(foreach ITEM,$(PERF_TEST_APPS),$(eval $(call genPerfAppImageKindPush,$(ITEM))))

# Enumerate test app build targets
BUILD_E2E_APPS_TARGETS:=$(foreach ITEM,$(E2E_TEST_APPS),build-e2e-app-$(ITEM))
# Enumerate test app push targets
PUSH_E2E_APPS_TARGETS:=$(foreach ITEM,$(E2E_TEST_APPS),push-e2e-app-$(ITEM))
# Enumerate test app build-push targets
BUILD_PUSH_E2E_APPS_TARGETS:=$(foreach ITEM,$(E2E_TEST_APPS),build-push-e2e-app-$(ITEM))
# Enumerate test app push targets
PUSH_KIND_E2E_APPS_TARGETS:=$(foreach ITEM,$(E2E_TEST_APPS),push-kind-e2e-app-$(ITEM))

# Enumerate test app build targets
BUILD_PERF_APPS_TARGETS:=$(foreach ITEM,$(PERF_TEST_APPS),build-perf-app-$(ITEM))
# Enumerate perf app push targets
PUSH_PERF_APPS_TARGETS:=$(foreach ITEM,$(PERF_TEST_APPS),push-perf-app-$(ITEM))
# Enumerate perf app build-push targets
BUILD_PUSH_PERF_APPS_TARGETS:=$(foreach ITEM,$(PERF_TEST_APPS),build-push-perf-app-$(ITEM))
# Enumerate perf app kind push targets
PUSH_KIND_PERF_APPS_TARGETS:=$(foreach ITEM,$(PERF_TEST_APPS),push-kind-perf-app-$(ITEM))

# build test app image
build-e2e-app-all: $(BUILD_E2E_APPS_TARGETS)

# push test app image to the registry
push-e2e-app-all: $(PUSH_E2E_APPS_TARGETS)

# build and push test app image to the registry
# can be faster because it uses cache and copies images directly
build-push-e2e-app-all: $(BUILD_PUSH_E2E_APPS_TARGETS)

# push test app image to kind cluster
push-kind-e2e-app-all: $(PUSH_KIND_E2E_APPS_TARGETS)

# build perf app image
build-perf-app-all: $(BUILD_PERF_APPS_TARGETS)

# push perf app image to the registry
push-perf-app-all: $(PUSH_PERF_APPS_TARGETS)

# build and push perf app image to the registry
# can be faster because it uses cache and copies images directly
build-push-perf-app-all: $(BUILD_PUSH_PERF_APPS_TARGETS)

# push perf app image to kind cluster
push-kind-perf-app-all: $(PUSH_KIND_PERF_APPS_TARGETS)

.PHONY: test-deps
test-deps:
	# The desire here is to download this test dependency without polluting go.mod
	command -v gotestsum || go install gotest.tools/gotestsum@latest

# start all e2e tests
test-e2e-all: check-e2e-env test-deps
	# Note: we can set -p 2 to run two tests apps at a time, because today we do not share state between
	# tests. In the future, if we add any tests that modify global state (such as dapr config), we'll
	# have to be sure and run them after the main test suite, so as not to alter the state of a running
	# test
	# Note2: use env variable DAPR_E2E_TEST to pick one e2e test to run.
     ifeq ($(DAPR_E2E_TEST),)
	DAPR_CONTAINER_LOG_PATH=$(DAPR_CONTAINER_LOG_PATH) DAPR_TEST_LOG_PATH=$(DAPR_TEST_LOG_PATH) GOOS=$(TARGET_OS_LOCAL) DAPR_TEST_NAMESPACE=$(DAPR_TEST_NAMESPACE) DAPR_TEST_TAG=$(DAPR_TEST_TAG) DAPR_TEST_REGISTRY=$(DAPR_TEST_REGISTRY) DAPR_TEST_MINIKUBE_IP=$(MINIKUBE_NODE_IP) gotestsum --jsonfile $(TEST_OUTPUT_FILE_PREFIX)_e2e.json --junitfile $(TEST_OUTPUT_FILE_PREFIX)_e2e.xml --format standard-quiet -- -timeout 20m -p 2 -count=1 -v -tags=e2e ./tests/e2e/$(DAPR_E2E_TEST)/...
     else
	for app in $(DAPR_E2E_TEST); do \
		DAPR_CONTAINER_LOG_PATH=$(DAPR_CONTAINER_LOG_PATH) DAPR_TEST_LOG_PATH=$(DAPR_TEST_LOG_PATH) GOOS=$(TARGET_OS_LOCAL) DAPR_TEST_NAMESPACE=$(DAPR_TEST_NAMESPACE) DAPR_TEST_TAG=$(DAPR_TEST_TAG) DAPR_TEST_REGISTRY=$(DAPR_TEST_REGISTRY) DAPR_TEST_MINIKUBE_IP=$(MINIKUBE_NODE_IP) gotestsum --jsonfile $(TEST_OUTPUT_FILE_PREFIX)_e2e.json --junitfile $(TEST_OUTPUT_FILE_PREFIX)_e2e.xml --format standard-quiet -- -timeout 20m -p 2 -count=1 -v -tags=e2e ./tests/e2e/$$app/...; \
	done
     endif

define genPerfTestRun
.PHONY: test-perf-$(1)
test-perf-$(1): check-e2e-env test-deps
	DAPR_CONTAINER_LOG_PATH=$(DAPR_CONTAINER_LOG_PATH) \
	DAPR_TEST_LOG_PATH=$(DAPR_TEST_LOG_PATH) \
	GOOS=$(TARGET_OS_LOCAL) \
	DAPR_TEST_NAMESPACE=$(DAPR_TEST_NAMESPACE) \
	DAPR_TEST_TAG=$(DAPR_TEST_TAG) \
	DAPR_TEST_REGISTRY=$(DAPR_TEST_REGISTRY) \
	DAPR_TEST_MINIKUBE_IP=$(MINIKUBE_NODE_IP) \
	NO_API_LOGGING=true \
		gotestsum \
			--jsonfile $(TEST_OUTPUT_FILE_PREFIX)_perf_$(1).json \
			--junitfile $(TEST_OUTPUT_FILE_PREFIX)_perf_$(1).xml \
			--format standard-quiet \
			-- \
				-timeout 2h -p 1 -count=1 -v -tags=perf ./tests/perf/$(1)/...
	jq -r .Output $(TEST_OUTPUT_FILE_PREFIX)_perf_$(1).json | strings
endef

# Generate perf app image build targets
$(foreach ITEM,$(PERF_TESTS),$(eval $(call genPerfTestRun,$(ITEM))))

TEST_PERF_TARGETS:=$(foreach ITEM,$(PERF_TESTS),test-perf-$(ITEM))

# start all perf tests
test-perf-all: check-e2e-env test-deps
	# Note: use env variable DAPR_PERF_TEST to pick one e2e test to run.
ifeq ($(DAPR_PERF_TEST),)
	DAPR_CONTAINER_LOG_PATH=$(DAPR_CONTAINER_LOG_PATH) \
	DAPR_TEST_LOG_PATH=$(DAPR_TEST_LOG_PATH) \
	GOOS=$(TARGET_OS_LOCAL) \
	DAPR_TEST_NAMESPACE=$(DAPR_TEST_NAMESPACE) \
	DAPR_TEST_TAG=$(DAPR_TEST_TAG) \
	DAPR_TEST_REGISTRY=$(DAPR_TEST_REGISTRY) \
	DAPR_TEST_MINIKUBE_IP=$(MINIKUBE_NODE_IP) \
	NO_API_LOGGING=true \
		gotestsum \
		--jsonfile $(TEST_OUTPUT_FILE_PREFIX)_perf.json \
		--junitfile $(TEST_OUTPUT_FILE_PREFIX)_perf.xml \
		--format standard-quiet \
		-- \
			-timeout 2.5h -p 1 -count=1 -v -tags=perf ./tests/perf/...
	jq -r .Output $(TEST_OUTPUT_FILE_PREFIX)_perf.json | strings
else
	for app in $(DAPR_PERF_TEST); do \
		DAPR_CONTAINER_LOG_PATH=$(DAPR_CONTAINER_LOG_PATH) \
		DAPR_TEST_LOG_PATH=$(DAPR_TEST_LOG_PATH) \
		GOOS=$(TARGET_OS_LOCAL) \
		DAPR_TEST_NAMESPACE=$(DAPR_TEST_NAMESPACE) \
		DAPR_TEST_TAG=$(DAPR_TEST_TAG) \
		DAPR_TEST_REGISTRY=$(DAPR_TEST_REGISTRY) \
		DAPR_TEST_MINIKUBE_IP=$(MINIKUBE_NODE_IP) \
		NO_API_LOGGING=true \
			gotestsum \
			--jsonfile $(TEST_OUTPUT_FILE_PREFIX)_perf.json \
			--junitfile $(TEST_OUTPUT_FILE_PREFIX)_perf.xml \
			--format standard-quiet \
			-- \
				-timeout 2.5h -p 1 -count=1 -v -tags=perf ./tests/perf/$$app... || exit -1 ; \
		jq -r .Output $(TEST_OUTPUT_FILE_PREFIX)_perf.json | strings ; \
	done
endif

test-perf-pubsub-subscribe-http-components: check-e2e-env test-deps
	DAPR_CONTAINER_LOG_PATH=$(DAPR_CONTAINER_LOG_PATH) \
	DAPR_TEST_LOG_PATH=$(DAPR_TEST_LOG_PATH) \
	GOOS=$(TARGET_OS_LOCAL) \
	DAPR_TEST_NAMESPACE=$(DAPR_TEST_NAMESPACE) \
	DAPR_TEST_TAG=$(DAPR_TEST_TAG) \
	DAPR_TEST_REGISTRY=$(DAPR_TEST_REGISTRY) \
	DAPR_TEST_MINIKUBE_IP=$(MINIKUBE_NODE_IP) \
	DAPR_PERF_PUBSUB_SUBS_HTTP_TEST_CONFIG_FILE_NAME=$(DAPR_PERF_PUBSUB_SUBS_HTTP_TEST_CONFIG_FILE_NAME) \
	NO_API_LOGGING=true \
		gotestsum \
			--jsonfile $(TEST_OUTPUT_FILE_PREFIX)_perf_$(1).json \
			--junitfile $(TEST_OUTPUT_FILE_PREFIX)_perf_$(1).xml \
			--format standard-quiet \
			-- \
				-timeout 3h -p 1 -count=1 -v -tags=perf ./tests/perf/pubsub_subscribe_http/...
	jq -r .Output $(TEST_OUTPUT_FILE_PREFIX)_perf_$(1).json | strings

# add required helm repo
setup-helm-init:
	$(HELM) repo add bitnami https://charts.bitnami.com/bitnami
	$(HELM) repo add stable https://charts.helm.sh/stable
	$(HELM) repo add incubator https://charts.helm.sh/incubator
	$(HELM) repo update

# setup tailscale
.PHONY: setup-tailscale
setup-tailscale:
ifeq ($(TAILSCALE_AUTH_KEY),)
	$(error TAILSCALE_AUTH_KEY environment variable must be set)
else
	DAPR_TEST_NAMESPACE=$(DAPR_TEST_NAMESPACE) TAILSCALE_AUTH_KEY=$(TAILSCALE_AUTH_KEY) ./tests/setup_tailscale.sh
endif

# install k6 loadtesting to the cluster
setup-test-env-k6: controller-gen
	$(KUBECTL) apply -f ./tests/config/k6_sa.yaml -n $(DAPR_TEST_NAMESPACE)
	$(KUBECTL) apply -f ./tests/config/k6_rolebinding.yaml -n $(DAPR_TEST_NAMESPACE)
	$(KUBECTL) apply -f ./tests/config/k6_sa_secret.yaml -n $(DAPR_TEST_NAMESPACE)
	# definitely not cruft - removing CRDs that have been deprecated
	export IMG=ghcr.io/grafana/operator:controller-v0.0.8 && \
	rm -rf /tmp/.k6-operator >/dev/null && \
	git clone --depth 1 --branch v0.0.8 https://github.com/grafana/k6-operator /tmp/.k6-operator && \
	cd /tmp/.k6-operator && \
	sed -i 's/crd:trivialVersions=true,crdVersions=v1/crd:maxDescLen=0/g' Makefile && \
	make deploy && \
	cd - && \
	rm -rf /tmp/.k6-operator
delete-test-env-k6: controller-gen
	$(KUBECTL) delete -f ./tests/config/k6_sa.yaml -n $(DAPR_TEST_NAMESPACE)
	$(KUBECTL) delete -f ./tests/config/k6_rolebinding.yaml -n $(DAPR_TEST_NAMESPACE)
	$(KUBECTL) delete -f ./tests/config/k6_sa_secret.yaml -n $(DAPR_TEST_NAMESPACE)
	rm -rf /tmp/.k6-operator >/dev/null && git clone https://github.com/grafana/k6-operator /tmp/.k6-operator && cd /tmp/.k6-operator && make delete && cd - && rm -rf /tmp/.k6-operator

# install redis to the cluster without password
setup-test-env-redis:
	$(HELM) upgrade \
	  --install dapr-redis bitnami/redis \
	  --version 17.14.5 \
	  --wait \
	  --timeout 5m0s \
	  --namespace $(DAPR_TEST_NAMESPACE) \
	 --set master.persistence.size=1Gi \
	  -f ./tests/config/redis_override.yaml

delete-test-env-redis:
	${HELM} del dapr-redis --namespace ${DAPR_TEST_NAMESPACE}

# install kafka to the cluster
setup-test-env-kafka:
	$(HELM) upgrade \
	  --install dapr-kafka bitnami/kafka \
	  --version 23.0.7 \
	  -f ./tests/config/kafka_override.yaml \
	  --namespace $(DAPR_TEST_NAMESPACE) \
	  --set broker.persistence.size=1Gi \
	  --set broker.logPersistence.size=1Gi \
	  --set zookeeper.persistence.size=1Gi \
	  --set image.repository=bitnamilegacy/kafka \
	  --timeout 10m0s

# install rabbitmq to the cluster
setup-test-env-rabbitmq:
	$(HELM) upgrade \
	  --install rabbitmq bitnami/rabbitmq \
	  --version 12.0.9 \
	  --set auth.username='admin' \
	  --set auth.password='admin' \
	  --namespace $(DAPR_TEST_NAMESPACE) \
	  --set persistence.size=1Gi \
	  --set image.repository=bitnamilegacy/rabbitmq \
	  --timeout 10m0s

# install mqtt to the cluster
setup-test-env-mqtt:
	$(HELM) repo add emqx https://repos.emqx.io/charts
	$(HELM) repo update
	$(HELM) upgrade \
	  --install perf-test-emqx emqx/emqx \
	  --version 5.1.4 \
	  --namespace $(DAPR_TEST_NAMESPACE) \
	  --timeout 10m0s

# install mqtt to the cluster
setup-test-env-pulsar:
	$(HELM) repo add apache https://pulsar.apache.org/charts
	$(HELM) repo update
	$(HELM) upgrade \
	  --install perf-test-pulsar apache/pulsar \
	  --version 3.0.0 \
	  --namespace $(DAPR_TEST_NAMESPACE) \
	  --timeout 10m0s

# delete kafka from cluster
delete-test-env-kafka:
	$(HELM) del dapr-kafka --namespace $(DAPR_TEST_NAMESPACE)

# install postgres to the cluster
setup-test-env-postgres:
	$(HELM) upgrade \
	  --install dapr-postgres bitnami/postgresql \
	  --version 12.8.0 \
	  -f ./tests/config/postgres_override.yaml \
	  --set primary.persistence.size=1Gi \
	  --set image.repository=bitnamilegacy/postgresql \
	  --namespace $(DAPR_TEST_NAMESPACE) \
	  --wait \
	  --timeout 5m0s

# delete postgres from cluster
delete-test-env-postgres:
	$(HELM) del dapr-postgres --namespace $(DAPR_TEST_NAMESPACE)

# install zipkin to the cluster
setup-test-env-zipkin:
	$(KUBECTL) apply -f ./tests/config/zipkin.yaml -n $(DAPR_TEST_NAMESPACE)
delete-test-env-zipkin:
	$(KUBECTL) delete -f ./tests/config/zipkin.yaml -n $(DAPR_TEST_NAMESPACE)

# Setup the test environment by installing components
setup-test-env: setup-test-env-kafka setup-test-env-redis setup-test-env-postgres setup-test-env-k6 setup-test-env-zipkin setup-test-env-postgres

save-dapr-control-plane-k8s-resources:
	mkdir -p '$(DAPR_CONTAINER_LOG_PATH)'
	$(KUBECTL) describe all -n $(DAPR_TEST_NAMESPACE) > '$(DAPR_CONTAINER_LOG_PATH)/control_plane_k8s_resources.txt'

save-dapr-control-plane-k8s-logs:
	mkdir -p '$(DAPR_CONTAINER_LOG_PATH)'
	$(KUBECTL) logs -l 'app.kubernetes.io/name=dapr' --tail=-1 -n $(DAPR_TEST_NAMESPACE) > '$(DAPR_CONTAINER_LOG_PATH)/control_plane_containers.log' --all-containers

# Apply default config yaml to turn mTLS off for testing (mTLS is enabled by default)
setup-disable-mtls:
	$(KUBECTL) apply -f ./tests/config/dapr_mtls_off_config.yaml --namespace $(DAPR_TEST_NAMESPACE)

# Apply default config yaml to turn tracing off for testing (tracing is enabled by default)
setup-app-configurations:
	$(KUBECTL) apply -f ./tests/config/dapr_observability_test_config.yaml --namespace $(DAPR_TEST_NAMESPACE)

# Apply component yaml for state, secrets, pubsub, workflows, and bindings
setup-test-components: setup-app-configurations
	$(KUBECTL) apply -f ./tests/config/kubernetes_secret.yaml --namespace $(DAPR_TEST_NAMESPACE)
	$(KUBECTL) apply -f ./tests/config/kubernetes_secret_config.yaml --namespace $(DAPR_TEST_NAMESPACE)
	$(KUBECTL) apply -f ./tests/config/kubernetes_redis_secret.yaml --namespace $(DAPR_TEST_NAMESPACE)
	$(KUBECTL) apply -f ./tests/config/kubernetes_redis_host_config.yaml --namespace $(DAPR_TEST_NAMESPACE)
	$(KUBECTL) apply -f ./tests/config/kubernetes_actor_reminder_scheduler_config.yaml --namespace $(DAPR_TEST_NAMESPACE)
	$(KUBECTL) apply -f ./tests/config/dapr_$(DAPR_TEST_STATE_STORE)_state.yaml --namespace $(DAPR_TEST_NAMESPACE)
	$(KUBECTL) apply -f ./tests/config/dapr_$(DAPR_TEST_STATE_STORE)_state_actorstore.yaml --namespace $(DAPR_TEST_NAMESPACE)
	$(KUBECTL) apply -f ./tests/config/dapr_$(DAPR_TEST_QUERY_STATE_STORE)_query_state.yaml --namespace $(DAPR_TEST_NAMESPACE)
	$(KUBECTL) apply -f ./tests/config/dapr_redis_pluggable_state.yaml --namespace $(DAPR_TEST_NAMESPACE)
	$(KUBECTL) apply -f ./tests/config/dapr_tests_cluster_role_binding.yaml --namespace $(DAPR_TEST_NAMESPACE)
	$(KUBECTL) apply -f ./tests/config/dapr_$(DAPR_TEST_PUBSUB)_pubsub.yaml --namespace $(DAPR_TEST_NAMESPACE)
	$(KUBECTL) apply -f ./tests/config/dapr_$(DAPR_TEST_CONFIG_STORE)_configuration.yaml --namespace $(DAPR_TEST_NAMESPACE)
	$(KUBECTL) apply -f ./tests/config/pubsub_no_resiliency.yaml --namespace $(DAPR_TEST_NAMESPACE)
	$(KUBECTL) apply -f ./tests/config/kafka_pubsub.yaml --namespace $(DAPR_TEST_NAMESPACE)
	$(KUBECTL) apply -f ./tests/config/dapr_crypto_$(DAPR_TEST_CRYPTO).yaml --namespace $(DAPR_TEST_NAMESPACE)
	$(KUBECTL) apply -f ./tests/config/dapr_kafka_pluggable_bindings.yaml --namespace $(DAPR_TEST_NAMESPACE)
	$(KUBECTL) apply -f ./tests/config/dapr_kafka_bindings.yaml --namespace $(DAPR_TEST_NAMESPACE)
	$(KUBECTL) apply -f ./tests/config/dapr_kafka_bindings_custom_route.yaml --namespace $(DAPR_TEST_NAMESPACE)
	$(KUBECTL) apply -f ./tests/config/dapr_kafka_bindings_grpc.yaml --namespace $(DAPR_TEST_NAMESPACE)
	$(KUBECTL) apply -f ./tests/config/app_topic_subscription_pluggable_pubsub.yaml --namespace $(DAPR_TEST_NAMESPACE)
	$(KUBECTL) apply -f ./tests/config/app_topic_subscription_pubsub.yaml --namespace $(DAPR_TEST_NAMESPACE)
	$(KUBECTL) apply -f ./tests/config/app_topic_subscription_pubsub_grpc.yaml --namespace $(DAPR_TEST_NAMESPACE)
	$(KUBECTL) apply -f ./tests/config/kubernetes_allowlists_config.yaml --namespace $(DAPR_TEST_NAMESPACE)
	$(KUBECTL) apply -f ./tests/config/kubernetes_allowlists_grpc_config.yaml --namespace $(DAPR_TEST_NAMESPACE)
	$(KUBECTL) apply -f ./tests/config/dapr_redis_state_query.yaml --namespace $(DAPR_TEST_NAMESPACE)
	$(KUBECTL) apply -f ./tests/config/dapr_redis_state_badhost.yaml --namespace $(DAPR_TEST_NAMESPACE)
	$(KUBECTL) apply -f ./tests/config/dapr_redis_state_badpass.yaml --namespace $(DAPR_TEST_NAMESPACE)
	$(KUBECTL) apply -f ./tests/config/dapr_vault_secretstore.yaml --namespace $(DAPR_TEST_NAMESPACE)
	$(KUBECTL) apply -f ./tests/config/uppercase.yaml --namespace $(DAPR_TEST_NAMESPACE)
	$(KUBECTL) apply -f ./tests/config/pipeline.yaml --namespace $(DAPR_TEST_NAMESPACE)
	$(KUBECTL) apply -f ./tests/config/pipeline_app.yaml --namespace $(DAPR_TEST_NAMESPACE)
	$(KUBECTL) apply -f ./tests/config/preview_configurations.yaml --namespace $(DAPR_TEST_NAMESPACE)
	$(KUBECTL) apply -f ./tests/config/app_topic_subscription_routing.yaml --namespace $(DAPR_TEST_NAMESPACE)
	$(KUBECTL) apply -f ./tests/config/app_topic_subscription_routing_grpc.yaml --namespace $(DAPR_TEST_NAMESPACE)
	$(KUBECTL) apply -f ./tests/config/resiliency.yaml --namespace $(DAPR_TEST_NAMESPACE)
	$(KUBECTL) apply -f ./tests/config/resiliency_kafka_bindings.yaml --namespace $(DAPR_TEST_NAMESPACE)
	$(KUBECTL) apply -f ./tests/config/resiliency_kafka_bindings_grpc.yaml --namespace $(DAPR_TEST_NAMESPACE)
	$(KUBECTL) apply -f ./tests/config/resiliency_$(DAPR_TEST_PUBSUB)_pubsub.yaml --namespace $(DAPR_TEST_NAMESPACE)
	$(KUBECTL) apply -f ./tests/config/dapr_in_memory_pubsub.yaml --namespace $(DAPR_TEST_NAMESPACE)
	$(KUBECTL) apply -f ./tests/config/dapr_in_memory_state.yaml --namespace $(DAPR_TEST_NAMESPACE)
	$(KUBECTL) apply -f ./tests/config/dapr_tracing_config.yaml --namespace $(DAPR_TEST_NAMESPACE)
	$(KUBECTL) apply -f ./tests/config/dapr_cron_binding.yaml --namespace $(DAPR_TEST_NAMESPACE)
	$(KUBECTL) apply -f ./tests/config/external_invocation_http_endpoint.yaml --namespace $(DAPR_TEST_NAMESPACE)
	$(KUBECTL) apply -f ./tests/config/grpcproxyserverexternal_service.yaml --namespace $(DAPR_TEST_NAMESPACE)
	$(KUBECTL) apply -f ./tests/config/externalinvocationcrd.yaml --namespace $(DAPR_TEST_NAMESPACE)
	$(KUBECTL) apply -f ./tests/config/omithealthchecks_config.yaml --namespace $(DAPR_TEST_NAMESPACE)
	$(KUBECTL) apply -f ./tests/config/external_invocation_http_endpoint_tls.yaml --namespace $(DAPR_TEST_NAMESPACE)
	# Don't set namespace as Namespace is defind in the yaml.
	$(KUBECTL) apply -f ./tests/config/ignore_daprsystem_config.yaml

	# Show the installed components
	$(KUBECTL) get components --namespace $(DAPR_TEST_NAMESPACE)

	# Show the installed configurations
	$(KUBECTL) get configurations --namespace $(DAPR_TEST_NAMESPACE)

setup-components-perf-test:
	$(KUBECTL) apply -f ./tests/config/pubsub_perf_components.yaml --namespace $(DAPR_TEST_NAMESPACE)

# Setup kind
setup-kind:
	kind create cluster --config ./tests/config/kind.yaml --name $(DAPR_TEST_KIND_CLUSTER_NAME)
	$(KUBECTL) cluster-info --context kind-$(DAPR_TEST_KIND_CLUSTER_NAME)
	# Setup registry
	docker run -d --restart=always -p $(DAPR_TEST_REGISTRY_PORT):$(DAPR_TEST_REGISTRY_PORT) --name kind-registry registry:2
	# Connect the registry to the KinD network.
	docker network connect "kind" kind-registry
	# Setup metrics-server
	helm upgrade --install metrics-server metrics-server/metrics-server --namespace kube-system --set=args={--kubelet-insecure-tls}

describe-kind-env:
	@echo "\
	export MINIKUBE_NODE_IP=`kubectl get nodes \
	    -lkubernetes.io/hostname!=kind-control-plane \
        -ojsonpath='{.items[0].status.addresses[?(@.type=="InternalIP")].address}'`\n\
	export DAPR_REGISTRY=$${DAPR_REGISTRY:-localhost:$(DAPR_TEST_REGISTRY_PORT)/dapr}\n\
	export DAPR_TEST_REGISTRY=$${DAPR_TEST_REGISTRY:-localhost:$(DAPR_TEST_REGISTRY_PORT)/dapr}\n\
	export DAPR_TAG=dev\n\
	export DAPR_NAMESPACE=dapr-tests\n\
	export DAPR_TEST_KIND_CLUSTER_NAME=$(DAPR_TEST_KIND_CLUSTER_NAME)"


delete-kind:
	docker stop kind-registry && docker rm kind-registry || echo "Could not delete registry."
	kind delete cluster --name kind

ifeq ($(OS),Windows_NT)
    detected_OS := windows
else
    detected_OS := $(shell sh -c 'uname 2>/dev/null || echo Unknown' |  tr '[:upper:]' '[:lower:]')
endif

setup-minikube-darwin:
ifeq ($(TARGET_ARCH_LOCAL),amd64)
	minikube start --memory=4g --cpus=4 --driver=hyperkit
else
# Install qemu and configure the dedicated network: https://minikube.sigs.k8s.io/docs/drivers/qemu/#networking
	minikube start --memory=4g --cpus=4 --driver=qemu --network socket_vmnet
endif
	minikube addons enable metrics-server

setup-minikube-windows:
	minikube start --memory=4g --cpus=4
	minikube addons enable metrics-server

setup-minikube-linux:
	minikube start --memory=4g --cpus=4
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

# Delete minikube
delete-minikube:
	minikube delete

# Delete all stored test results
.PHONY: test-clean
test-clean:
	-rm -rv ./tests/e2e/*/dist
	-rm -rv ./tests/perf/*/dist
	-rm -rv ./tests/perf/*/test_report_summary_table_*.json
	-rm test_report_*.json
	-rm test_report_*.xml
