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
# Syntax: ./install-dapr-tools.sh [DAPR_CLI_VERSION] [PROTOC_VERSION] [PROTOC_GEN_GO_VERSION] [GOLANGCI_LINT_VERSION]

DAPR_CLI_VERSION=${1:-""}
PROTOC_VERSION=${2:-"21.1"}
PROTOC_GEN_GO_VERSION=${3:-"1.28"}
GOLANGCI_LINT_VERSION=${4:-"1.45.2"}

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

# Install protoc-gen-go and golangci-lint
go install "google.golang.org/protobuf/cmd/protoc-gen-go@v${PROTOC_GEN_GO_VERSION}"
go install "github.com/golangci/golangci-lint/cmd/golangci-lint@v${GOLANGCI_LINT_VERSION}"
