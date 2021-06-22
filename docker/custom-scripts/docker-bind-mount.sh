#!/usr/bin/env bash
# ------------------------------------------------------------
# Copyright (c) Microsoft Corporation and Dapr Contributors.
# Licensed under the MIT License.
# ------------------------------------------------------------

set -e

SOCAT_PATH_BASE=/tmp/vscr-docker-from-docker
SOCAT_LOG=${SOCAT_PATH_BASE}.log
SOCAT_PID=${SOCAT_PATH_BASE}.pid

USERNAME=$(whoami)
SOURCE_SOCKET=/var/run/docker-host.sock
TARGET_SOCKET=/var/run/docker.sock

apt-get-update-if-needed()
{
    if [ ! -d "/var/lib/apt/lists" ] || [ "$(ls /var/lib/apt/lists/ | wc -l)" = "0" ]; then
        echo "Running apt-get update..."
        sudo apt-get update
    else
        echo "Skipping apt-get update."
    fi
}

# Log messages
log()
{
    echo -e "[$(date)] $@" | sudo tee -a ${SOCAT_LOG} > /dev/null
}

log "Ensuring ${USERNAME} has access to ${SOURCE_SOCKET} via ${TARGET_SOCKET}"

# If enabled, try to add a docker group with the right GID. If the group is root,
# fall back on using socat to forward the docker socket to another unix socket so
# that we can set permissions on it without affecting the host.
SOCKET_GID=$(stat -c '%g' ${SOURCE_SOCKET})
if [ "${SOCKET_GID}" != "0" ]; then
    log "Adding user to group with GID ${SOCKET_GID}."
    if [ "$(cat /etc/group | grep :${SOCKET_GID}:)" = "" ]; then
        sudo groupadd --gid ${SOCKET_GID} docker-host
    fi
    # Add user to group if not already in it
    if [ "$(id ${USERNAME} | grep -E "groups.*(=|,)${SOCKET_GID}\(")" = "" ]; then
        sudo usermod -aG ${SOCKET_GID} ${USERNAME}
    fi
    # Map the source socket to the target socket, if needed
    if [ -h "${TARGET_SOCKET}" ]; then
        sudo rm -rf ${TARGET_SOCKET}
        sudo touch "${SOURCE_SOCKET}"
        ln -s "${SOURCE_SOCKET}" "${TARGET_SOCKET}"
    fi
else
    # Enable proxy if not already running
    if [ ! -f "${SOCAT_PID}" ] || ! ps -p $(cat ${SOCAT_PID}) > /dev/null; then
        if ! dpkg -s socat > /dev/null 2>&1; then
            log "Installing required socat packages ..."
            apt-get-update-if-needed
            sudo apt-get -y install socat
        fi
        log "Proxying ${SOURCE_SOCKET} to ${TARGET_SOCKET} for vscode"
        sudo rm -rf ${TARGET_SOCKET}
        (sudo socat UNIX-LISTEN:${TARGET_SOCKET},fork,mode=660,user=${USERNAME} UNIX-CONNECT:${SOURCE_SOCKET} 2>&1 | sudo tee -a ${SOCAT_LOG} > /dev/null & echo "$!" | sudo tee ${SOCAT_PID} > /dev/null)
    else
        log "Socket proxy already running."
    fi
fi
log "Success"

# Execute whatever commands were passed in (if any). This allows us
# to set this script to ENTRYPOINT while still executing the default CMD.
set +e
exec "$@"
