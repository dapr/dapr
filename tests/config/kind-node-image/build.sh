#!/usr/bin/env bash

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

# Build custom KinD node image with pre-baked 3rd-party container images.
#
# Uses "helm template" to resolve the actual container images that the Helm
# charts deploy, so the pre-baked images always match what tests expect.
#
# Usage:
#   ./build.sh [K8S_VERSION] [IMAGE_TAG]
#
# Arguments:
#   K8S_VERSION  Kubernetes version for the base kindest/node image (default: v1.34.0)
#   IMAGE_TAG    Tag for the output image (default: ghcr.io/dapr/kind-node:${K8S_VERSION})
#
# Examples:
#   ./build.sh                           # Build for default K8s version
#   ./build.sh v1.33.4                   # Build for K8s 1.33.4
#   ./build.sh v1.34.0 my-image:latest   # Build with custom output tag
#
# Environment variables (optional, override resolved image references):
#   REDIS_IMAGE       Redis image (default: resolved from redis_override.yaml)
#   KAFKA_IMAGE       Kafka image (default: resolved from helm template)
#   ZOOKEEPER_IMAGE   Zookeeper image (default: resolved from helm template)
#   POSTGRESQL_IMAGE  PostgreSQL image (default: resolved from helm template)
#   ZIPKIN_IMAGE      Zipkin image (default: resolved from zipkin.yaml)

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/../../.." && pwd)"

K8S_VERSION="${1:-v1.34.0}"
IMAGE_TAG="${2:-ghcr.io/dapr/kind-node:${K8S_VERSION}}"

# Resolve image references from config files and Helm charts if not overridden.

if [[ -z "${REDIS_IMAGE:-}" ]]; then
  # Redis image is explicit in the override file.
  REDIS_REPO=$(grep 'repository:' "${REPO_ROOT}/tests/config/redis_override.yaml" | head -1 | awk '{print $2}')
  REDIS_TAG=$(grep 'tag:' "${REPO_ROOT}/tests/config/redis_override.yaml" | head -1 | awk '{print $2}')
  REDIS_IMAGE="docker.io/${REDIS_REPO}:${REDIS_TAG}"
fi

if [[ -z "${ZIPKIN_IMAGE:-}" ]]; then
  # Zipkin image is explicit in the manifest.
  ZIPKIN_IMAGE=$(grep 'image:' "${REPO_ROOT}/tests/config/zipkin.yaml" | head -1 | awk '{print $2}')
fi

if [[ -z "${KAFKA_IMAGE:-}" ]] || [[ -z "${ZOOKEEPER_IMAGE:-}" ]]; then
  echo "Resolving Kafka/Zookeeper images from Helm chart bitnami/kafka 23.0.7..."
  helm repo add bitnami https://charts.bitnami.com/bitnami 2>/dev/null || true
  helm repo update bitnami 2>/dev/null

  KAFKA_TEMPLATE=$(helm template dapr-kafka bitnami/kafka \
    --version 23.0.7 \
    -f "${REPO_ROOT}/tests/config/kafka_override.yaml" \
    --set image.repository=bitnamilegacy/kafka 2>/dev/null)

  if [[ -z "${KAFKA_IMAGE:-}" ]]; then
    KAFKA_IMAGE=$(echo "${KAFKA_TEMPLATE}" | grep 'image:' | grep 'kafka' | head -1 | awk '{print $2}' | tr -d '"')
    # Ensure docker.io prefix for containerd
    [[ "${KAFKA_IMAGE}" != docker.io/* ]] && KAFKA_IMAGE="docker.io/${KAFKA_IMAGE}"
  fi
  if [[ -z "${ZOOKEEPER_IMAGE:-}" ]]; then
    ZOOKEEPER_IMAGE=$(echo "${KAFKA_TEMPLATE}" | grep 'image:' | grep 'zookeeper' | head -1 | awk '{print $2}' | tr -d '"')
    [[ "${ZOOKEEPER_IMAGE}" != docker.io/* ]] && ZOOKEEPER_IMAGE="docker.io/${ZOOKEEPER_IMAGE}"
  fi
fi

if [[ -z "${POSTGRESQL_IMAGE:-}" ]]; then
  echo "Resolving PostgreSQL image from Helm chart bitnami/postgresql 12.8.0..."
  helm repo add bitnami https://charts.bitnami.com/bitnami 2>/dev/null || true

  POSTGRES_TEMPLATE=$(helm template dapr-postgres bitnami/postgresql \
    --version 12.8.0 \
    -f "${REPO_ROOT}/tests/config/postgres_override.yaml" \
    --set image.repository=bitnamilegacy/postgresql 2>/dev/null)

  POSTGRESQL_IMAGE=$(echo "${POSTGRES_TEMPLATE}" | grep 'image:' | grep 'postgresql' | head -1 | awk '{print $2}' | tr -d '"')
  [[ "${POSTGRESQL_IMAGE}" != docker.io/* ]] && POSTGRESQL_IMAGE="docker.io/${POSTGRESQL_IMAGE}"
fi

echo "Building custom KinD node image"
echo "  Base K8s version: ${K8S_VERSION}"
echo "  Output image:     ${IMAGE_TAG}"
echo "  Redis:            ${REDIS_IMAGE}"
echo "  Kafka:            ${KAFKA_IMAGE}"
echo "  Zookeeper:        ${ZOOKEEPER_IMAGE}"
echo "  PostgreSQL:       ${POSTGRESQL_IMAGE}"
echo "  Zipkin:           ${ZIPKIN_IMAGE}"

docker build \
  --build-arg "K8S_VERSION=${K8S_VERSION}" \
  --build-arg "REDIS_IMAGE=${REDIS_IMAGE}" \
  --build-arg "KAFKA_IMAGE=${KAFKA_IMAGE}" \
  --build-arg "ZOOKEEPER_IMAGE=${ZOOKEEPER_IMAGE}" \
  --build-arg "POSTGRESQL_IMAGE=${POSTGRESQL_IMAGE}" \
  --build-arg "ZIPKIN_IMAGE=${ZIPKIN_IMAGE}" \
  -t "${IMAGE_TAG}" \
  -f "${SCRIPT_DIR}/Dockerfile" \
  "${SCRIPT_DIR}"

echo ""
echo "Successfully built: ${IMAGE_TAG}"
echo "To push: docker push ${IMAGE_TAG}"
