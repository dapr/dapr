#!/usr/bin/env bash
# ------------------------------------------------------------
# Copyright (c) Microsoft Corporation and Dapr Contributors.
# Licensed under the MIT License.
# ------------------------------------------------------------

set -e
echo "Running devcontainer-init.sh ..."

# Clone kubectl and minikube config from host if requested when running local devcontainer.
if [[ "${SYNC_LOCALHOST_KUBECONFIG,,}" == "true" && "${CODESPACES,,}" != "true" ]]; then
    mkdir -p ${HOME}/.kube
    if [ -d "${HOME}/.kube-localhost" ]; then
        cp -r ${HOME}/.kube-localhost/* ${HOME}/.kube
    fi
    if [ -d "${HOME}/.minikube-localhost" ]; then
        mkdir -p ${HOME}/.minikube
        if [ -r ${HOME}/.minikube-localhost/ca.crt ]; then
            cp -r ${HOME}/.minikube-localhost/ca.crt ${HOME}/.minikube
            sed -i -r "s|(\s*certificate-authority:\s).*|\\1${HOME}\/.minikube\/ca.crt|g" ${HOME}/.kube/config
        fi
        if [ -r ${HOME}/.minikube-localhost/client.crt ]; then
            cp -r ${HOME}/.minikube-localhost/client.crt ${HOME}/.minikube
            sed -i -r "s|(\s*client-certificate:\s).*|\\1${HOME}\/.minikube\/client.crt|g" ${HOME}/.kube/config
        fi
        if [ -r ${HOME}/.minikube-localhost/client.key ]; then
            cp -r ${HOME}/.minikube-localhost/client.key ${HOME}/.minikube
            sed -i -r "s|(\s*client-key:\s).*|\\1${HOME}\/.minikube\/client.key|g" ${HOME}/.kube/config
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
