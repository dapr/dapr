#!/usr/bin/env bash

# Source: Adapted from https://github.com/microsoft/vscode-dev-containers/blob/v0.224.3/script-library/docker-debian.sh

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

set -e

# Wrapper function to only use sudo if not already root
sudoIf()
{
    if [ "$(id -u)" -ne 0 ]; then
        sudo "$@"
    else
        "$@"
    fi
}

### Diff start
USERNAME=$(whoami)
SOURCE_SOCKET=/var/run/docker-host.sock
TARGET_SOCKET=/var/run/docker.sock
ENABLE_NONROOT_DOCKER="true"

if [ "${SOURCE_SOCKET}" != "${TARGET_SOCKET}" ]; then
    sudoIf touch "${SOURCE_SOCKET}"
    sudoIf ln -s "${SOURCE_SOCKET}" "${TARGET_SOCKET}"
fi
### Diff end

SOCAT_PATH_BASE=/tmp/vscr-docker-from-docker
SOCAT_LOG=${SOCAT_PATH_BASE}.log
SOCAT_PID=${SOCAT_PATH_BASE}.pid

# Log messages
log()
{
    echo -e "[$(date)] $@" | sudoIf tee -a ${SOCAT_LOG} > /dev/null
}

echo -e "\n** $(date) **" | sudoIf tee -a ${SOCAT_LOG} > /dev/null
log "Ensuring ${USERNAME} has access to ${SOURCE_SOCKET} via ${TARGET_SOCKET}"

# If enabled, try to add a docker group with the right GID. If the group is root, 
# fall back on using socat to forward the docker socket to another unix socket so 
# that we can set permissions on it without affecting the host.
if [ "${ENABLE_NONROOT_DOCKER}" = "true" ] && [ "${SOURCE_SOCKET}" != "${TARGET_SOCKET}" ] && [ "${USERNAME}" != "root" ] && [ "${USERNAME}" != "0" ]; then
    SOCKET_GID=$(stat -c '%g' ${SOURCE_SOCKET})
    if [ "${SOCKET_GID}" != "0" ]; then
        log "Adding user to group with GID ${SOCKET_GID}."
        if [ "$(cat /etc/group | grep :${SOCKET_GID}:)" = "" ]; then
            sudoIf groupadd --gid ${SOCKET_GID} docker-host
        fi
        # Add user to group if not already in it
        if [ "$(id ${USERNAME} | grep -E "groups.*(=|,)${SOCKET_GID}\(")" = "" ]; then
            sudoIf usermod -aG ${SOCKET_GID} ${USERNAME}
        fi
    else
        # Enable proxy if not already running
        if [ ! -f "${SOCAT_PID}" ] || ! ps -p $(cat ${SOCAT_PID}) > /dev/null; then
            log "Enabling socket proxy."
            log "Proxying ${SOURCE_SOCKET} to ${TARGET_SOCKET} for vscode"
            sudoIf rm -rf ${TARGET_SOCKET}
            (sudoIf socat UNIX-LISTEN:${TARGET_SOCKET},fork,mode=660,user=${USERNAME} UNIX-CONNECT:${SOURCE_SOCKET} 2>&1 | sudoIf tee -a ${SOCAT_LOG} > /dev/null & echo "$!" | sudoIf tee ${SOCAT_PID} > /dev/null)
        else
            log "Socket proxy already running."
        fi
    fi
    log "Success"
fi

# Execute whatever commands were passed in (if any). This allows us
# to set this script to ENTRYPOINT while still executing the default CMD.
set +e
exec "$@"
