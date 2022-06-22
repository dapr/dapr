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

# Source: https://github.com/microsoft/vscode-dev-containers/blob/v0.224.3/script-library/go-debian.sh

#-------------------------------------------------------------------------------------------------------------
# Copyright (c) Microsoft Corporation. All rights reserved.
# Licensed under the MIT License. See https://go.microsoft.com/fwlink/?linkid=2090316 for license information.
#-------------------------------------------------------------------------------------------------------------
#
# Docs: https://github.com/microsoft/vscode-dev-containers/blob/main/script-library/docs/go.md
# Maintainer: The VS Code and Codespaces Teams
#
# Syntax: ./go-debian.sh [Go version] [GOROOT] [GOPATH] [non-root user] [Add GOPATH, GOROOT to rc files flag] [Install tools flag]

TARGET_GO_VERSION=${1:-"latest"}
TARGET_GOROOT=${2:-"/usr/local/go"}
TARGET_GOPATH=${3:-"/go"}
USERNAME=${4:-"automatic"}
UPDATE_RC=${5:-"true"}
INSTALL_GO_TOOLS=${6:-"true"}

# https://www.google.com/linuxrepositories/
GO_GPG_KEY_URI="https://dl.google.com/linux/linux_signing_key.pub"

set -e

if [ "$(id -u)" -ne 0 ]; then
    echo -e 'Script must be run as root. Use sudo, su, or add "USER root" to your Dockerfile before running this script.'
    exit 1
fi

# Ensure that login shells get the correct path if the user updated the PATH using ENV.
rm -f /etc/profile.d/00-restore-env.sh
echo "export PATH=${PATH//$(sh -lc 'echo $PATH')/\$PATH}" > /etc/profile.d/00-restore-env.sh
chmod +x /etc/profile.d/00-restore-env.sh

# Determine the appropriate non-root user
if [ "${USERNAME}" = "auto" ] || [ "${USERNAME}" = "automatic" ]; then
    USERNAME=""
    POSSIBLE_USERS=("vscode" "node" "codespace" "$(awk -v val=1000 -F ":" '$3==val{print $1}' /etc/passwd)")
    for CURRENT_USER in ${POSSIBLE_USERS[@]}; do
        if id -u ${CURRENT_USER} > /dev/null 2>&1; then
            USERNAME=${CURRENT_USER}
            break
        fi
    done
    if [ "${USERNAME}" = "" ]; then
        USERNAME=root
    fi
elif [ "${USERNAME}" = "none" ] || ! id -u ${USERNAME} > /dev/null 2>&1; then
    USERNAME=root
fi

updaterc() {
    if [ "${UPDATE_RC}" = "true" ]; then
        echo "Updating /etc/bash.bashrc and /etc/zsh/zshrc..."
        if [[ "$(cat /etc/bash.bashrc)" != *"$1"* ]]; then
            echo -e "$1" >> /etc/bash.bashrc
        fi
        if [ -f "/etc/zsh/zshrc" ] && [[ "$(cat /etc/zsh/zshrc)" != *"$1"* ]]; then
            echo -e "$1" >> /etc/zsh/zshrc
        fi
    fi
}
# Figure out correct version of a three part version number is not passed
find_version_from_git_tags() {
    local variable_name=$1
    local requested_version=${!variable_name}
    if [ "${requested_version}" = "none" ]; then return; fi
    local repository=$2
    local prefix=${3:-"tags/v"}
    local separator=${4:-"."}
    local last_part_optional=${5:-"false"}    
    if [ "$(echo "${requested_version}" | grep -o "." | wc -l)" != "2" ]; then
        local escaped_separator=${separator//./\\.}
        local last_part
        if [ "${last_part_optional}" = "true" ]; then
            last_part="(${escaped_separator}[0-9]+)?"
        else
            last_part="${escaped_separator}[0-9]+"
        fi
        local regex="${prefix}\\K[0-9]+${escaped_separator}[0-9]+${last_part}$"
        local version_list="$(git ls-remote --tags ${repository} | grep -oP "${regex}" | tr -d ' ' | tr "${separator}" "." | sort -rV)"
        if [ "${requested_version}" = "latest" ] || [ "${requested_version}" = "current" ] || [ "${requested_version}" = "lts" ]; then
            declare -g ${variable_name}="$(echo "${version_list}" | head -n 1)"
        else
            set +e
            declare -g ${variable_name}="$(echo "${version_list}" | grep -E -m 1 "^${requested_version//./\\.}([\\.\\s]|$)")"
            set -e
        fi
    fi
    if [ -z "${!variable_name}" ] || ! echo "${version_list}" | grep "^${!variable_name//./\\.}$" > /dev/null 2>&1; then
        echo -e "Invalid ${variable_name} value: ${requested_version}\nValid values:\n${version_list}" >&2
        exit 1
    fi
    echo "${variable_name}=${!variable_name}"
}

# Get central common setting
get_common_setting() {
    if [ "${common_settings_file_loaded}" != "true" ]; then
        curl -sfL "https://aka.ms/vscode-dev-containers/script-library/settings.env" 2>/dev/null -o /tmp/vsdc-settings.env || echo "Could not download settings file. Skipping."
        common_settings_file_loaded=true
    fi
    if [ -f "/tmp/vsdc-settings.env" ]; then
        local multi_line=""
        if [ "$2" = "true" ]; then multi_line="-z"; fi
        local result="$(grep ${multi_line} -oP "$1=\"?\K[^\"]+" /tmp/vsdc-settings.env | tr -d '\0')"
        if [ ! -z "${result}" ]; then declare -g $1="${result}"; fi
    fi
    echo "$1=${!1}"
}

# Function to run apt-get if needed
apt_get_update_if_needed()
{
    if [ ! -d "/var/lib/apt/lists" ] || [ "$(ls /var/lib/apt/lists/ | wc -l)" = "0" ]; then
        echo "Running apt-get update..."
        apt-get update
    else
        echo "Skipping apt-get update."
    fi
}

# Checks if packages are installed and installs them if not
check_packages() {
    if ! dpkg -s "$@" > /dev/null 2>&1; then
        apt_get_update_if_needed
        apt-get -y install --no-install-recommends "$@"
    fi
}

export DEBIAN_FRONTEND=noninteractive

# Install curl, tar, git, other dependencies if missing
check_packages curl ca-certificates gnupg2 tar g++ gcc libc6-dev make pkg-config
if ! type git > /dev/null 2>&1; then
    apt_get_update_if_needed
    apt-get -y install --no-install-recommends git
fi

# Get closest match for version number specified
find_version_from_git_tags TARGET_GO_VERSION "https://go.googlesource.com/go" "tags/go" "." "true"

architecture="$(uname -m)"
case $architecture in
    x86_64) architecture="amd64";;
    aarch64 | armv8*) architecture="arm64";;
    aarch32 | armv7* | armvhf*) architecture="armv6l";;
    i?86) architecture="386";;
    *) echo "(!) Architecture $architecture unsupported"; exit 1 ;;
esac

# Install Go
umask 0002
if ! cat /etc/group | grep -e "^golang:" > /dev/null 2>&1; then
    groupadd -r golang
fi
usermod -a -G golang "${USERNAME}"
mkdir -p "${TARGET_GOROOT}" "${TARGET_GOPATH}" 
if [ "${TARGET_GO_VERSION}" != "none" ] && ! type go > /dev/null 2>&1; then
    # Use a temporary locaiton for gpg keys to avoid polluting image
    export GNUPGHOME="/tmp/tmp-gnupg"
    mkdir -p ${GNUPGHOME}
    chmod 700 ${GNUPGHOME}
    get_common_setting GO_GPG_KEY_URI
    curl -sSL -o /tmp/tmp-gnupg/golang_key "${GO_GPG_KEY_URI}"
    gpg -q --import /tmp/tmp-gnupg/golang_key
    echo "Downloading Go ${TARGET_GO_VERSION}..."
    set +e
    curl -fsSL -o /tmp/go.tar.gz "https://golang.org/dl/go${TARGET_GO_VERSION}.linux-${architecture}.tar.gz"
    exit_code=$?
    set -e
    if [ "$exit_code" != "0" ]; then
        echo "(!) Download failed."
        # Try one break fix version number less if we get a failure
        major="$(echo "${TARGET_GO_VERSION}" | grep -oE '^[0-9]+' || echo '')"
        minor="$(echo "${TARGET_GO_VERSION}" | grep -oP '^[0-9]+\.\K[0-9]+' || echo '')"
        breakfix="$(echo "${TARGET_GO_VERSION}" | grep -oP '^[0-9]+\.[0-9]+\.\K[0-9]+' 2>/dev/null || echo '')"
        if [ "${breakfix}" = "" ] || [ "${breakfix}" = "0" ]; then
            ((minor=minor-1))
            TARGET_GO_VERSION="${major}.${minor}"
            find_version_from_git_tags TARGET_GO_VERSION "https://go.googlesource.com/go" "tags/go" "." "true"
        else 
            ((breakfix=breakfix-1))
           TARGET_GO_VERSION="${major}.${minor}.${breakfix}"
        fi
        echo "Trying ${TARGET_GO_VERSION}..."
        curl -fsSL -o /tmp/go.tar.gz "https://golang.org/dl/go${TARGET_GO_VERSION}.linux-${architecture}.tar.gz"
    fi
    curl -fsSL -o /tmp/go.tar.gz.asc "https://golang.org/dl/go${TARGET_GO_VERSION}.linux-${architecture}.tar.gz.asc"
    gpg --verify /tmp/go.tar.gz.asc /tmp/go.tar.gz
    echo "Extracting Go ${TARGET_GO_VERSION}..."
    tar -xzf /tmp/go.tar.gz -C "${TARGET_GOROOT}" --strip-components=1
    rm -rf /tmp/go.tar.gz /tmp/go.tar.gz.asc /tmp/tmp-gnupg
else
    echo "Go already installed. Skipping."
fi

# Install Go tools that are isImportant && !replacedByGopls based on
# https://github.com/golang/vscode-go/blob/v0.31.1/src/goToolsInformation.ts
GO_TOOLS="\
    golang.org/x/tools/gopls@latest \
    honnef.co/go/tools/cmd/staticcheck@latest \
    golang.org/x/lint/golint@latest \
    github.com/mgechev/revive@latest \
    github.com/uudashr/gopkgs/v2/cmd/gopkgs@latest \
    github.com/ramya-rao-a/go-outline@latest \
    github.com/go-delve/delve/cmd/dlv@latest \
    github.com/josharian/impl@latest \
    github.com/fatih/gomodifytags@latest"
if [ "${INSTALL_GO_TOOLS}" = "true" ]; then
    echo "Installing common Go tools..."
    export PATH=${TARGET_GOROOT}/bin:${PATH}
    mkdir -p /tmp/gotools /usr/local/etc/vscode-dev-containers ${TARGET_GOPATH}/bin
    cd /tmp/gotools
    export GOPATH=/tmp/gotools
    export GOCACHE=/tmp/gotools/cache

    # Use go get for versions of go under 1.16
    go_install_command=install
    if [[ "1.16" > "$(go version | grep -oP 'go\K[0-9]+\.[0-9]+(\.[0-9]+)?')" ]]; then
        export GO111MODULE=on
        go_install_command=get
        echo "Go version < 1.16, using go get."
    fi 

    (echo "${GO_TOOLS}" | xargs -n 1 go ${go_install_command} -v )2>&1 | tee -a /usr/local/etc/vscode-dev-containers/go.log

    # Move Go tools into path and clean up
    mv /tmp/gotools/bin/* ${TARGET_GOPATH}/bin/

    rm -rf /tmp/gotools
fi

# Add GOPATH variable and bin directory into PATH in bashrc/zshrc files (unless disabled)
updaterc "$(cat << EOF
export GOPATH="${TARGET_GOPATH}"
if [[ "\${PATH}" != *"\${GOPATH}/bin"* ]]; then export PATH="\${PATH}:\${GOPATH}/bin"; fi
export GOROOT="${TARGET_GOROOT}"
if [[ "\${PATH}" != *"\${GOROOT}/bin"* ]]; then export PATH="\${PATH}:\${GOROOT}/bin"; fi
EOF
)"

chown -R :golang "${TARGET_GOROOT}" "${TARGET_GOPATH}"
chmod -R g+r+w "${TARGET_GOROOT}" "${TARGET_GOPATH}"
find "${TARGET_GOROOT}" -type d | xargs -n 1 chmod g+s
find "${TARGET_GOPATH}" -type d | xargs -n 1 chmod g+s

echo "Done!"
