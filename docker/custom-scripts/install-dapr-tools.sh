#!/usr/bin/env bash
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
#
# Syntax: ./install-dapr-tools.sh [USERNAME] [GOROOT] [GOPATH] [DAPR_CLI_VERSION] [PROTOC_VERSION] [PROTOC_GEN_GO_VERSION] [PROTOC_GEN_GO_GRPC_VERSION] [GOLANGCI_LINT_VERSION]

USERNAME=${1:-"dapr"}
GOROOT=${2:-"/usr/local/go"}
GOPATH=${3:-"/go"}
DAPR_CLI_VERSION=${4:-""}
PROTOC_VERSION=${5:-"21.12"}
PROTOC_GEN_GO_VERSION=${6:-"1.28.1"}
PROTOC_GEN_GO_GRPC_VERSION=${7:-"1.2.0"}
GOLANGCI_LINT_VERSION=${8:-"1.55.2"}

set -e

if [ "$(id -u)" -ne 0 ]; then
    echo -e 'Script must be run as root. Use sudo, su, or add "USER root" to your Dockerfile before running this script.'
    exit 1
fi

# Install socat
apt-get install -y socat

# Install Dapr CLI
dapr_cli_ver=""
if [ "${DAPR_CLI_VERSION}" != "latest" ]; then
    dapr_cli_ver="${DAPR_CLI_VERSION}"
fi
wget -q https://raw.githubusercontent.com/dapr/cli/master/install/install.sh -O - | /bin/bash -s "${dapr_cli_ver}"

# Install protoc compiler required by 'make gen-proto'
architecture="$(uname -m)"
case $architecture in
    x86_64) architecture="x86_64";;
    aarch64 | armv8*) architecture="aarch_64";;
    i?86) architecture="x86_32";;
    *) echo "(!) Architecture $architecture unsupported"; exit 1 ;;
esac

PROTOC_ZIP=protoc-${PROTOC_VERSION}-linux-${architecture}.zip
curl -LO "https://github.com/protocolbuffers/protobuf/releases/download/v${PROTOC_VERSION}/${PROTOC_ZIP}"
unzip -o "${PROTOC_ZIP}" -d /usr/local bin/protoc
chmod -R 755 /usr/local/bin/protoc
unzip -o "${PROTOC_ZIP}" -d /usr/local 'include/*'
chmod -R 755 /usr/local/include/google/protobuf
rm -f "${PROTOC_ZIP}"

# Install protoc-gen-go and protoc-gen-go-grpc
# Must be installed as the non-root user
export GOBIN="${GOPATH}/bin"
sudo -u ${USERNAME} --preserve-env=GOPATH,GOBIN,GOROOT \
    go install "google.golang.org/protobuf/cmd/protoc-gen-go@v${PROTOC_GEN_GO_VERSION}"
sudo -u ${USERNAME} --preserve-env=GOPATH,GOBIN,GOROOT \
    go install "google.golang.org/grpc/cmd/protoc-gen-go-grpc@v${PROTOC_GEN_GO_GRPC_VERSION}"

# Install golangci-lint using the recommended method (best to avoid using go install according to their docs)
# Must be installed as the non-root user
sudo -u ${USERNAME} --preserve-env=GOLANGCI_LINT_VERSION,GOPATH,GOBIN,GOROOT \
    sh -c 'curl -sSfL https://raw.githubusercontent.com/golangci/golangci-lint/master/install.sh | sh -s -- -b "${GOBIN}" "v${GOLANGCI_LINT_VERSION}"'
