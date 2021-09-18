#!/usr/bin/env bash
# ------------------------------------------------------------
# Copyright (c) Microsoft Corporation and Dapr Contributors.
# Licensed under the MIT License.
# ------------------------------------------------------------
#
# Syntax: ./install-go-tools.sh [USERNAME]

USERNAME=${1:-"dapr"}

set -e

if [ "$(id -u)" -ne 0 ]; then
    echo -e 'Script must be run as root. Use sudo, su, or add "USER root" to your Dockerfile before running this script.'
    exit 1
fi

TARGET_GOPATH=$(go env GOPATH)

# Install Go tools that are isImportant && !replacedByGopls based on
# https://github.com/golang/vscode-go/blob/0c6dce4a96978f61b022892c1376fe3a00c27677/src/goTools.ts#L188
# Exceptions:
#  - golangci-lint isImportant but installed using their install script below.
#  - gotests, impl and gomodifytags are !isImportant but included as convenience tools.
#  - protoc-gen-go is installed as a dependency for dapr development.
GO_TOOLS="\
    golang.org/x/tools/gopls@latest \
    honnef.co/go/tools/...@latest \
    golang.org/x/lint/golint@latest \
    github.com/mgechev/revive@latest \
    github.com/uudashr/gopkgs/v2/cmd/gopkgs@latest \
    github.com/ramya-rao-a/go-outline@latest \
    github.com/go-delve/delve/cmd/dlv@latest \
    github.com/cweill/gotests/...@latest \
    github.com/josharian/impl@latest \
    github.com/fatih/gomodifytags@latest \
    github.com/golang/protobuf/protoc-gen-go@latest"

echo "Installing Go tools for Dapr..."
mkdir -p /usr/local/etc/vscode-dev-containers ${TARGET_GOPATH}/bin

# Install Go tools w/module support using v1.16 `go install`
(echo "${GO_TOOLS}" | xargs -n 1 go install -v )2>&1 | tee -a /usr/local/etc/vscode-dev-containers/gotools.log

# Set up `dlv` as `dlv-dap` for VSCode go debugging
cp ${TARGET_GOPATH}/bin/dlv ${TARGET_GOPATH}/bin/dlv-dap

# Install golangci-lint
curl -sSfL https://raw.githubusercontent.com/golangci/golangci-lint/master/install.sh | sh -s -- -b ${TARGET_GOPATH}/bin 2>&1

# Set up Go path and dapr source folder for non-root user
mkdir -p ${TARGET_GOPATH}/src/github.com/dapr
chown -R ${USERNAME} "${TARGET_GOPATH}"

echo "Done!"
