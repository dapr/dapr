#
# Copyright 2026 The Dapr Authors
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

# Custom KinD node image with pre-baked 3rd-party container images.
#
# Pre-pulling these images into the node image eliminates container pull time
# during E2E test setup, saving ~15-20 minutes per test run.
#
# Pre-baked images (update these when Helm chart versions change):
#   - Redis:      redislabs/rejson:2.0.11          (tests/config/redis_override.yaml)
#   - Kafka:      bitnamilegacy/kafka               (tests/dapr_tests.mk, chart 23.0.7)
#   - Zookeeper:  bitnami/zookeeper                 (kafka chart 23.0.7 dependency)
#   - PostgreSQL: bitnamilegacy/postgresql           (tests/dapr_tests.mk, chart 12.8.0)
#   - Zipkin:     ghcr.io/dapr/3rdparty/zipkin      (tests/config/zipkin.yaml)

ARG K8S_VERSION=v1.34.0
FROM kindest/node:${K8S_VERSION} AS base

# Image references for pre-pulling.
# These must match what the Helm charts and manifests actually deploy.
#
# Redis and Zipkin tags are explicit in config files. Kafka, Zookeeper, and
# PostgreSQL tags come from Helm chart defaults — run build.sh which uses
# "helm template" to extract the actual image references automatically.
#
# Sources:
#   Redis:      tests/config/redis_override.yaml
#   Kafka:      tests/dapr_tests.mk (chart bitnami/kafka 23.0.7)
#   Zookeeper:  tests/dapr_tests.mk (kafka chart 23.0.7 dependency)
#   PostgreSQL: tests/dapr_tests.mk (chart bitnami/postgresql 12.8.0)
#   Zipkin:     tests/config/zipkin.yaml
#
# All images are required build args — there are no defaults, to avoid
# silently pulling wrong versions. Use build.sh to resolve them automatically.
ARG REDIS_IMAGE
ARG KAFKA_IMAGE
ARG ZOOKEEPER_IMAGE
ARG POSTGRESQL_IMAGE
ARG ZIPKIN_IMAGE

# Start containerd, pull all required images into the k8s.io namespace
# (which is where Kubernetes/CRI looks for images), then stop containerd.
# Using tmpfs mounts for /run and /tmp since containerd needs them for runtime
# state but we don't want to persist that in the image layer.
RUN --mount=type=tmpfs,target=/run \
    --mount=type=tmpfs,target=/tmp \
    containerd -log-level warn & \
    while ! ctr version >/dev/null 2>&1; do sleep 0.1; done && \
    echo "Pulling Redis image: ${REDIS_IMAGE}" && \
    ctr -n k8s.io images pull ${REDIS_IMAGE} && \
    echo "Pulling Kafka image: ${KAFKA_IMAGE}" && \
    ctr -n k8s.io images pull ${KAFKA_IMAGE} && \
    echo "Pulling Zookeeper image: ${ZOOKEEPER_IMAGE}" && \
    ctr -n k8s.io images pull ${ZOOKEEPER_IMAGE} && \
    echo "Pulling PostgreSQL image: ${POSTGRESQL_IMAGE}" && \
    ctr -n k8s.io images pull ${POSTGRESQL_IMAGE} && \
    echo "Pulling Zipkin image: ${ZIPKIN_IMAGE}" && \
    ctr -n k8s.io images pull ${ZIPKIN_IMAGE} && \
    echo "All images pulled successfully." && \
    kill $(pidof containerd) && \
    wait
