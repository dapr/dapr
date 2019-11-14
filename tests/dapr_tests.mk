# ------------------------------------------------------------
# Copyright (c) Microsoft Corporation.
# Licensed under the MIT License.
# ------------------------------------------------------------

# set TEST_PLATFORM to minikube by default unless TEST_PLATFORM is set
ifneq ($(TEST_PLATFORM),)
	TEST_PLATFORM=minikube
endif

# check the required environment variables
check-e2e-env:
ifeq ($(DAPR_TEST_REGISTRY),)
	$(error DAPR_TEST_REGISTRY environment variable must be set)
endif
ifeq ($(DAPR_TEST_TAG),)
	$(error DAPR_TEST_TAG environment variable must be set)
endif

# build test app image
build-e2e-apps: check-docker-env
	$(info Building $(DOCKER_IMAGE_TAG) docker image ...)
	$(DOCKER) build -f $(DOCKERFILE_DIR)/$(DOCKERFILE) $(LINUX_BINS_OUT_DIR)/. -t $(DOCKER_IMAGE_TAG)

# push test app image to the registry
push-e2e-apps: build-e2e-apps
	$(info Pushing $(DOCKER_IMAGE_TAG) docker image ...)
	$(DOCKER) push $(DOCKER_IMAGE_TAG)

test-e2e-all:
	go test -v -tags=e2e ./tests/e2e/...
