#!/usr/bin/env bash
#-------------------------------------------------------------------------------------------------------------
# Copyright (c) Microsoft Corporation. All rights reserved.
# Licensed under the MIT License. See https://go.microsoft.com/fwlink/?linkid=2090316 for license information.
#-------------------------------------------------------------------------------------------------------------
#
# Docs: https://github.com/microsoft/vscode-dev-containers/blob/master/script-library/docs/kubectl-helm.md
# Maintainer: The VS Code and Codespaces Teams
#
# Syntax: ./kubectl-helm-debian.sh [kubectl version] [Helm version] [minikube version] [kubectl SHA256] [Helm SHA256] [minikube SHA256]

set -e

KUBECTL_VERSION="${1:-"latest"}"
HELM_VERSION="${2:-"latest"}"
MINIKUBE_VERSION="${3:-"none"}" # latest is also valid
KUBECTL_SHA256="${4:-"automatic"}"
HELM_SHA256="${5:-"automatic"}"
MINIKUBE_SHA256="${6:-"automatic"}"

if [ "$(id -u)" -ne 0 ]; then
    echo -e 'Script must be run as root. Use sudo, su, or add "USER root" to your Dockerfile before running this script.'
    exit 1
fi

export DEBIAN_FRONTEND=noninteractive

# Install curl if missing
if ! dpkg -s curl ca-certificates gnupg2 > /dev/null 2>&1; then
    if [ ! -d "/var/lib/apt/lists" ] || [ "$(ls /var/lib/apt/lists/ | wc -l)" = "0" ]; then
        apt-get update
    fi
    apt-get -y install --no-install-recommends curl ca-certificates gnupg2
fi

ARCHITECTURE="$(uname -m)"
case $ARCHITECTURE in
    armv*) ARCHITECTURE="arm";;
    aarch64) ARCHITECTURE="arm64";;
    x86_64) ARCHITECTURE="amd64";;
esac

# Install the kubectl, verify checksum
echo "Downloading kubectl..."
if [ "${KUBECTL_VERSION}" = "latest" ] || [ "${KUBECTL_VERSION}" = "lts" ] || [ "${KUBECTL_VERSION}" = "current" ] || [ "${KUBECTL_VERSION}" = "stable" ]; then
    KUBECTL_VERSION="$(curl -sSL https://dl.k8s.io/release/stable.txt)"
fi
if [ "${KUBECTL_VERSION::1}" != 'v' ]; then
    KUBECTL_VERSION="v${KUBECTL_VERSION}"
fi
curl -sSL -o /usr/local/bin/kubectl "https://dl.k8s.io/release/${KUBECTL_VERSION}/bin/linux/${ARCHITECTURE}/kubectl"
chmod 0755 /usr/local/bin/kubectl
if [ "$KUBECTL_SHA256" = "automatic" ]; then
    KUBECTL_SHA256="$(curl -sSL "https://dl.k8s.io/${KUBECTL_VERSION}/bin/linux/${ARCHITECTURE}/kubectl.sha256")"
fi
([ "${KUBECTL_SHA256}" = "dev-mode" ] || (echo "${KUBECTL_SHA256} */usr/local/bin/kubectl" | sha256sum -c -))
if ! type kubectl > /dev/null 2>&1; then
    echo '(!) kubectl installation failed!'
    exit 1
fi

# Install Helm, verify signature and checksum
echo "Downloading Helm..."
if [ "${HELM_VERSION}" = "latest" ] || [ "${HELM_VERSION}" = "lts" ] || [ "${HELM_VERSION}" = "current" ]; then
    HELM_VERSION=$(basename "$(curl -fsSL -o /dev/null -w "%{url_effective}" https://github.com/helm/helm/releases/latest)")
fi
if [ "${HELM_VERSION::1}" != 'v' ]; then
    HELM_VERSION="v${HELM_VERSION}"
fi
mkdir -p /tmp/helm
HELM_FILENAME="helm-${HELM_VERSION}-linux-${ARCHITECTURE}.tar.gz"
TMP_HELM_FILENAME="/tmp/helm/${HELM_FILENAME}"
curl -sSL "https://get.helm.sh/${HELM_FILENAME}" -o "${TMP_HELM_FILENAME}"
curl -sSL "https://github.com/helm/helm/releases/download/${HELM_VERSION}/${HELM_FILENAME}.asc" -o "${TMP_HELM_FILENAME}.asc"
# todo - use aka.ms for keys
curl -sSL "https://raw.githubusercontent.com/helm/helm/main/KEYS" -o /tmp/helm/KEYS
echo "disable-ipv6" >> /tmp/helm/dirmngr.conf
export GNUPGHOME="/tmp/helm/gnupg"
mkdir -p "${GNUPGHOME}"
chmod 700 ${GNUPGHOME}
gpg -q --import "/tmp/helm/KEYS"
if ! gpg --verify "${TMP_HELM_FILENAME}.asc" > /tmp/helm/gnupg/verify.log 2>&1; then
    echo "Verification failed!"
    cat /tmp/helm/gnupg/verify.log
    exit 1
fi
if [ "${HELM_SHA256}" = "automatic" ]; then
    curl -sSL "https://get.helm.sh/${HELM_FILENAME}.sha256" -o "${TMP_HELM_FILENAME}.sha256"
    curl -sSL "https://github.com/helm/helm/releases/download/${HELM_VERSION}/${HELM_FILENAME}.sha256.asc" -o "${TMP_HELM_FILENAME}.sha256.asc"
    if ! gpg --verify "${TMP_HELM_FILENAME}.sha256.asc" > /tmp/helm/gnupg/verify.log 2>&1; then
        echo "Verification failed!"
        cat /tmp/helm/gnupg/verify.log
        exit 1
    fi
    HELM_SHA256="$(cat "${TMP_HELM_FILENAME}.sha256")"
fi
([ "${HELM_SHA256}" = "dev-mode" ] || (echo "${HELM_SHA256} *${TMP_HELM_FILENAME}" | sha256sum -c -))
tar xf "${TMP_HELM_FILENAME}" -C /tmp/helm
mv -f "/tmp/helm/linux-${ARCHITECTURE}/helm" /usr/local/bin/
chmod 0755 /usr/local/bin/helm
rm -rf /tmp/helm
if ! type helm > /dev/null 2>&1; then
    echo '(!) Helm installation failed!'
    exit 1
fi

# Install Minikube, verify checksum
if [ "${MINIKUBE_VERSION}" != "none" ]; then
    echo "Downloading minikube..."
    # latest is also valid in the download URLs 
    if [ "${MINIKUBE_VERSION}" != "latest" ] && [ "${MINIKUBE_VERSION::1}" != "v" ]; then
        MINIKUBE_VERSION="v${MINIKUBE_VERSION}"
    fi
    curl -sSL -o /usr/local/bin/minikube "https://storage.googleapis.com/minikube/releases/${MINIKUBE_VERSION}/minikube-linux-${ARCHITECTURE}"    
    chmod 0755 /usr/local/bin/minikube
    if [ "$MINIKUBE_SHA256" = "automatic" ]; then
        MINIKUBE_SHA256="$(curl -sSL "https://storage.googleapis.com/minikube/releases/${MINIKUBE_VERSION}/minikube-linux-${ARCHITECTURE}.sha256")"
    fi
    ([ "${MINIKUBE_SHA256}" = "dev-mode" ] || (echo "${MINIKUBE_SHA256} */usr/local/bin/minikube" | sha256sum -c -))
    if ! type minikube > /dev/null 2>&1; then
        echo '(!) minikube installation failed!'
        exit 1
    fi
fi

if ! type docker > /dev/null 2>&1; then
    echo -e '\n(*) Warning: The docker command was not found.\n\nYou can use one of the following scripts to install it:\n\nhttps://github.com/microsoft/vscode-dev-containers/blob/master/script-library/docs/docker-in-docker.md\n\nor\n\nhttps://github.com/microsoft/vscode-dev-containers/blob/master/script-library/docs/docker.md'
fi

echo -e "\nDone!"