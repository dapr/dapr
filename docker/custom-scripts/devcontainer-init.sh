#!/usr/bin/env bash

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
# Initializes the devcontainer tasks each time the container starts.
# Users can edit this copy under /usr/local/share in the container to
# customize this as needed for their custom localhost bindings.

set -e
echo "Running devcontainer-init.sh ..."

# Clone kubectl and minikube config from host if requested when running local devcontainer.
if [[ "${SYNC_LOCALHOST_KUBECONFIG,,}" == "true" && "${CODESPACES,,}" != "true" ]]; then
    mkdir -p ${HOME}/.kube
    if [ -d "${HOME}/.kube-localhost" ]; then
        cp -r ${HOME}/.kube-localhost/* ${HOME}/.kube
    fi

    # [EXPERIMENTAL] As a convenience feature, when using localhost minikube cluster in the devcontainer,
    # attempt to clone the credentials from the default localhost .minikube profile and fixup
    # the container's copy of .kube/config with the correct endpoint and path to cloned credentials.
    # It does not support modifying the minikube configuration from the container (minikube needs to already
    # be started on the local host) and assumes the only kubernetes context pointing to a localhost
    # server (i.e. 127.0.0.1 address) belongs to the minikube default profile and should be updated.

    if [ -d "${HOME}/.minikube-localhost" ]; then
        mkdir -p ${HOME}/.minikube
        if [ -r ${HOME}/.minikube-localhost/ca.crt ]; then
            cp -r ${HOME}/.minikube-localhost/ca.crt ${HOME}/.minikube
            sed -i -r "s|(\s*certificate-authority:\s).*|\\1${HOME}\/.minikube\/ca.crt|g" ${HOME}/.kube/config
        fi
        if [ -r ${HOME}/.minikube-localhost/profiles/minikube/client.crt ]; then
            cp -r ${HOME}/.minikube-localhost/profiles/minikube/client.crt ${HOME}/.minikube
            sed -i -r "s|(\s*client-certificate:\s).*|\\1${HOME}\/.minikube\/client.crt|g" ${HOME}/.kube/config
        fi
        if [ -r ${HOME}/.minikube-localhost/profiles/minikube/client.key ]; then
            cp -r ${HOME}/.minikube-localhost/profiles/minikube/client.key ${HOME}/.minikube
            sed -i -r "s|(\s*client-key:\s).*|\\1${HOME}\/.minikube\/client.key|g" ${HOME}/.kube/config
        fi
        if [ -r ${HOME}/.minikube-localhost/profiles/minikube/config.json ]; then
            ENDPOINT=$(grep -E '\"IP\":|\"Port\":' ${HOME}/.minikube-localhost/profiles/minikube/config.json \
                | sed -r '{N;s/\s*\"IP\": \"(.+)\",\s*\"Port\": ([0-9]*),/\1:\2/;}')
            sed -i -r 's/(server: https:\/\/)127.0.0.1:[0-9]*(.*)/\1'"${ENDPOINT}"'\2/' ${HOME}/.kube/config
        fi
    fi
fi

# Invoke /usr/local/share/docker-bind-mount.sh or docker-init.sh as appropriate
set +e
if [[ "${BIND_LOCALHOST_DOCKER,,}" == "true" ]]; then
    echo "Invoking docker-bind-mount.sh ..."
    exec /usr/local/share/docker-bind-mount.sh "$@"
else
    echo "Invoking docker-init.sh ..."
    exec /usr/local/share/docker-init.sh "$@"
fi
