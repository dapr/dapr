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
