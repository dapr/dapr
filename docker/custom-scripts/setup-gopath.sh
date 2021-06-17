#!/usr/bin/env bash
# ------------------------------------------------------------
# Copyright (c) Microsoft Corporation and Dapr Contributors.
# Licensed under the MIT License.
# ------------------------------------------------------------
#
# Syntax: ./init-gopath.sh [LINK_DAPR_PROJECT] [CLONE_DAPR_REPO]

LINK_DAPR_PROJECT=${1:-"dapr"}
CLONE_DAPR_REPO=${2:-"false"}

set -e

if [ "$(id -u)" -eq 0 ]; then
    echo -e 'Script must be run as sudo-capable non-root user.'
    exit 1
fi

TARGET_GOPATH=$(go env GOPATH)

# Clone dapr/dapr repo into GOPATH alongside components-contrib if requested
# and there isn't a valid git repo bind mounted there already.
if [[ ${CLONE_DAPR_REPO,,} == "true" && ! -d ${TARGET_GOPATH}/src/github.com/dapr/dapr/.git ]]; then
    echo "Cloning dapr/dapr repo ..."
    sudo chown dapr ${TARGET_GOPATH}/src/github.com/dapr/dapr
    git clone https://github.com/dapr/dapr ${TARGET_GOPATH}/src/github.com/dapr/dapr
fi

# If running in Codespaces, workspaceFolder is ignored, so link the default
# Codespaces workspaces folder to under the GOPATH instead as a workaround.
if [[ ${CODESPACES,,} == "true" && ! -d ${TARGET_GOPATH}/src/github.com/dapr/${LINK_DAPR_PROJECT}/.git ]]; then
    echo "Creating link to workspace folder under ${TARGET_GOPATH} ..."
    ln -s /workspaces/${LINK_DAPR_PROJECT} ${TARGET_GOPATH}/src/github.com/dapr/${LINK_DAPR_PROJECT}
fi

echo "Done!"
