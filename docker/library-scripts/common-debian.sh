#!/usr/bin/env bash
#-------------------------------------------------------------------------------------------------------------
# Copyright (c) Microsoft Corporation. All rights reserved.
# Licensed under the MIT License. See https://go.microsoft.com/fwlink/?linkid=2090316 for license information.
#-------------------------------------------------------------------------------------------------------------
#
# Docs: https://github.com/microsoft/vscode-dev-containers/blob/master/script-library/docs/common.md
# Maintainer: The VS Code and Codespaces Teams
#
# Syntax: ./common-debian.sh [install zsh flag] [username] [user UID] [user GID] [upgrade packages flag] [install Oh My Zsh! flag] [Add non-free packages]

INSTALL_ZSH=${1:-"true"}
USERNAME=${2:-"automatic"}
USER_UID=${3:-"automatic"}
USER_GID=${4:-"automatic"}
UPGRADE_PACKAGES=${5:-"true"}
INSTALL_OH_MYS=${6:-"true"}
ADD_NON_FREE_PACKAGES=${7:-"false"}

set -e

if [ "$(id -u)" -ne 0 ]; then
    echo -e 'Script must be run as root. Use sudo, su, or add "USER root" to your Dockerfile before running this script.'
    exit 1
fi

# Ensure that login shells get the correct path if the user updated the PATH using ENV.
rm -f /etc/profile.d/00-restore-env.sh
echo "export PATH=${PATH//$(sh -lc 'echo $PATH')/\$PATH}" > /etc/profile.d/00-restore-env.sh
chmod +x /etc/profile.d/00-restore-env.sh

# If in automatic mode, determine if a user already exists, if not use vscode
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
        USERNAME=vscode
    fi
elif [ "${USERNAME}" = "none" ]; then
    USERNAME=root
    USER_UID=0
    USER_GID=0
fi

# Load markers to see which steps have already run
MARKER_FILE="/usr/local/etc/vscode-dev-containers/common"
if [ -f "${MARKER_FILE}" ]; then
    echo "Marker file found:"
    cat "${MARKER_FILE}"
    source "${MARKER_FILE}"
fi

# Ensure apt is in non-interactive to avoid prompts
export DEBIAN_FRONTEND=noninteractive

# Function to call apt-get if needed
apt-get-update-if-needed()
{
    if [ ! -d "/var/lib/apt/lists" ] || [ "$(ls /var/lib/apt/lists/ | wc -l)" = "0" ]; then
        echo "Running apt-get update..."
        apt-get update
    else
        echo "Skipping apt-get update."
    fi
}

# Run install apt-utils to avoid debconf warning then verify presence of other common developer tools and dependencies
if [ "${PACKAGES_ALREADY_INSTALLED}" != "true" ]; then

    PACKAGE_LIST="apt-utils \
        git \
        openssh-client \
        gnupg2 \
        iproute2 \
        procps \
        lsof \
        htop \
        net-tools \
        psmisc \
        curl \
        wget \
        rsync \
        ca-certificates \
        unzip \
        zip \
        nano \
        vim-tiny \
        less \
        jq \
        lsb-release \
        apt-transport-https \
        dialog \
        libc6 \
        libgcc1 \
        libkrb5-3 \
        libgssapi-krb5-2 \
        libicu[0-9][0-9] \
        liblttng-ust0 \
        libstdc++6 \
        zlib1g \
        locales \
        sudo \
        ncdu \
        man-db \
        strace \
        manpages \
        manpages-dev \
        init-system-helpers"
        
    # Needed for adding manpages-posix and manpages-posix-dev which are non-free packages in Debian
    if [ "${ADD_NON_FREE_PACKAGES}" = "true" ]; then
        CODENAME="$(cat /etc/os-release | grep -oE '^VERSION_CODENAME=.+$' | cut -d'=' -f2)"
        sed -i "s/deb http:\/\/deb\.debian\.org\/debian ${CODENAME} main/deb http:\/\/deb\.debian\.org\/debian ${CODENAME} main contrib non-free/" /etc/apt/sources.list
        sed -i "s/deb-src http:\/\/deb\.debian\.org\/debian ${CODENAME} main/deb http:\/\/deb\.debian\.org\/debian ${CODENAME} main contrib non-free/" /etc/apt/sources.list
        sed -i "s/deb http:\/\/deb\.debian\.org\/debian ${CODENAME}-updates main/deb http:\/\/deb\.debian\.org\/debian ${CODENAME}-updates main contrib non-free/" /etc/apt/sources.list
        sed -i "s/deb-src http:\/\/deb\.debian\.org\/debian ${CODENAME}-updates main/deb http:\/\/deb\.debian\.org\/debian ${CODENAME}-updates main contrib non-free/" /etc/apt/sources.list
        sed -i "s/deb http:\/\/security\.debian\.org\/debian-security ${CODENAME}\/updates main/deb http:\/\/security\.debian\.org\/debian-security ${CODENAME}\/updates main contrib non-free/" /etc/apt/sources.list
        sed -i "s/deb-src http:\/\/security\.debian\.org\/debian-security ${CODENAME}\/updates main/deb http:\/\/security\.debian\.org\/debian-security ${CODENAME}\/updates main contrib non-free/" /etc/apt/sources.list
        sed -i "s/deb http:\/\/deb\.debian\.org\/debian ${CODENAME}-backports main/deb http:\/\/deb\.debian\.org\/debian ${CODENAME}-backports main contrib non-free/" /etc/apt/sources.list 
        sed -i "s/deb-src http:\/\/deb\.debian\.org\/debian ${CODENAME}-backports main/deb http:\/\/deb\.debian\.org\/debian ${CODENAME}-backports main contrib non-free/" /etc/apt/sources.list
        echo "Running apt-get update..."
        apt-get update
        PACKAGE_LIST="${PACKAGE_LIST} manpages-posix manpages-posix-dev"
    else
        apt-get-update-if-needed
    fi

    # Install libssl1.1 if available
    if [[ ! -z $(apt-cache --names-only search ^libssl1.1$) ]]; then
        PACKAGE_LIST="${PACKAGE_LIST}       libssl1.1"
    fi
    
    # Install appropriate version of libssl1.0.x if available
    LIBSSL=$(dpkg-query -f '${db:Status-Abbrev}\t${binary:Package}\n' -W 'libssl1\.0\.?' 2>&1 || echo '')
    if [ "$(echo "$LIBSSL" | grep -o 'libssl1\.0\.[0-9]:' | uniq | sort | wc -l)" -eq 0 ]; then
        if [[ ! -z $(apt-cache --names-only search ^libssl1.0.2$) ]]; then
            # Debian 9
            PACKAGE_LIST="${PACKAGE_LIST}       libssl1.0.2"
        elif [[ ! -z $(apt-cache --names-only search ^libssl1.0.0$) ]]; then
            # Ubuntu 18.04, 16.04, earlier
            PACKAGE_LIST="${PACKAGE_LIST}       libssl1.0.0"
        fi
    fi

    echo "Packages to verify are installed: ${PACKAGE_LIST}"
    apt-get -y install --no-install-recommends ${PACKAGE_LIST} 2> >( grep -v 'debconf: delaying package configuration, since apt-utils is not installed' >&2 )
        
    PACKAGES_ALREADY_INSTALLED="true"
fi

# Get to latest versions of all packages
if [ "${UPGRADE_PACKAGES}" = "true" ]; then
    apt-get-update-if-needed
    apt-get -y upgrade --no-install-recommends
    apt-get autoremove -y
fi

# Ensure at least the en_US.UTF-8 UTF-8 locale is available.
# Common need for both applications and things like the agnoster ZSH theme.
if [ "${LOCALE_ALREADY_SET}" != "true" ] && ! grep -o -E '^\s*en_US.UTF-8\s+UTF-8' /etc/locale.gen > /dev/null; then
    echo "en_US.UTF-8 UTF-8" >> /etc/locale.gen 
    locale-gen
    LOCALE_ALREADY_SET="true"
fi

# Create or update a non-root user to match UID/GID.
if id -u ${USERNAME} > /dev/null 2>&1; then
    # User exists, update if needed
    if [ "${USER_GID}" != "automatic" ] && [ "$USER_GID" != "$(id -G $USERNAME)" ]; then 
        groupmod --gid $USER_GID $USERNAME 
        usermod --gid $USER_GID $USERNAME
    fi
    if [ "${USER_UID}" != "automatic" ] && [ "$USER_UID" != "$(id -u $USERNAME)" ]; then 
        usermod --uid $USER_UID $USERNAME
    fi
else
    # Create user
    if [ "${USER_GID}" = "automatic" ]; then
        groupadd $USERNAME
    else
        groupadd --gid $USER_GID $USERNAME
    fi
    if [ "${USER_UID}" = "automatic" ]; then 
        useradd -s /bin/bash --gid $USERNAME -m $USERNAME
    else
        useradd -s /bin/bash --uid $USER_UID --gid $USERNAME -m $USERNAME
    fi
fi

# Add add sudo support for non-root user
if [ "${USERNAME}" != "root" ] && [ "${EXISTING_NON_ROOT_USER}" != "${USERNAME}" ]; then
    echo $USERNAME ALL=\(root\) NOPASSWD:ALL > /etc/sudoers.d/$USERNAME
    chmod 0440 /etc/sudoers.d/$USERNAME
    EXISTING_NON_ROOT_USER="${USERNAME}"
fi

# ** Shell customization section **
if [ "${USERNAME}" = "root" ]; then 
    USER_RC_PATH="/root"
else
    USER_RC_PATH="/home/${USERNAME}"
fi

# .bashrc/.zshrc snippet
RC_SNIPPET="$(cat << 'EOF'

if [ -z "${USER}" ]; then export USER=$(whoami); fi
if [[ "${PATH}" != *"$HOME/.local/bin"* ]]; then export PATH="${PATH}:$HOME/.local/bin"; fi

# Display optional first run image specific notice if configured and terminal is interactive
if [ -t 1 ] && [ ! -f "$HOME/.config/vscode-dev-containers/first-run-notice-already-displayed" ]; then
    if [ -f "/usr/local/etc/vscode-dev-containers/first-run-notice.txt" ]; then
        cat "/usr/local/etc/vscode-dev-containers/first-run-notice.txt"
    elif [ -f "/workspaces/.codespaces/shared/first-run-notice.txt" ]; then
        cat "/workspaces/.codespaces/shared/first-run-notice.txt"
    fi
    mkdir -p "$HOME/.config/vscode-dev-containers"
    # Mark first run notice as displayed after 10s to avoid problems with fast terminal refreshes hiding it
    ((sleep 10s; touch "$HOME/.config/vscode-dev-containers/first-run-notice-already-displayed") &)
fi

EOF
)"

# code shim, it fallbacks to code-insiders if code is not available
cat << 'EOF' > /usr/local/bin/code
#!/bin/sh

get_in_path_except_current() {
    which -a "$1" | grep -A1 "$0" | grep -v "$0"
}

code="$(get_in_path_except_current code)"

if [ -n "$code" ]; then
    exec "$code" "$@"
elif [ "$(command -v code-insiders)" ]; then
    exec code-insiders "$@"
else
    echo "code or code-insiders is not installed" >&2
    exit 127
fi
EOF
chmod +x /usr/local/bin/code

# systemctl shim - tells people to use 'service' if systemd is not running
cat << 'EOF' > /usr/local/bin/systemctl
#!/bin/sh
set -e
if [ -d "/run/systemd/system" ]; then
    exec /bin/systemctl/systemctl "$@"
else
    echo '\n"systemd" is not running in this container due to its overhead.\nUse the "service" command to start services intead. e.g.: \n\nservice --status-all'
fi
EOF
chmod +x /usr/local/bin/systemctl

# Codespaces bash and OMZ themes - partly inspired by https://github.com/ohmyzsh/ohmyzsh/blob/master/themes/robbyrussell.zsh-theme
CODESPACES_BASH="$(cat \
<<'EOF'

# Codespaces bash prompt theme
__bash_prompt() {
    local userpart='`export XIT=$? \
        && [ ! -z "${GITHUB_USER}" ] && echo -n "\[\033[0;32m\]@${GITHUB_USER} " || echo -n "\[\033[0;32m\]\u " \
        && [ "$XIT" -ne "0" ] && echo -n "\[\033[1;31m\]➜" || echo -n "\[\033[0m\]➜"`'
    local gitbranch='`\
        export BRANCH=$(git rev-parse --abbrev-ref HEAD 2>/dev/null); \
        if [ "${BRANCH}" = "HEAD" ]; then \
            export BRANCH=$(git describe --contains --all HEAD 2>/dev/null); \
        fi; \
        if [ "${BRANCH}" != "" ]; then \
            echo -n "\[\033[0;36m\](\[\033[1;31m\]${BRANCH}" \
            && if git ls-files --error-unmatch -m --directory --no-empty-directory -o --exclude-standard ":/*" > /dev/null 2>&1; then \
                    echo -n " \[\033[1;33m\]✗"; \
            fi \
            && echo -n "\[\033[0;36m\]) "; \
        fi`'
    local lightblue='\[\033[1;34m\]'
    local removecolor='\[\033[0m\]'
    PS1="${userpart} ${lightblue}\w ${gitbranch}${removecolor}\$ "
    unset -f __bash_prompt
}
__bash_prompt

EOF
)"
CODESPACES_ZSH="$(cat \
<<'EOF'
__zsh_prompt() {
    local prompt_username
    if [ ! -z "${GITHUB_USER}" ]; then 
        prompt_username="@${GITHUB_USER}"
    else
        prompt_username="%n"
    fi
    PROMPT="%{$fg[green]%}${prompt_username} %(?:%{$reset_color%}➜ :%{$fg_bold[red]%}➜ )" # User/exit code arrow
    PROMPT+='%{$fg_bold[blue]%}%(5~|%-1~/…/%3~|%4~)%{$reset_color%} ' # cwd
    PROMPT+='$(git_prompt_info)%{$fg[white]%}$ %{$reset_color%}' # Git status
    unset -f __zsh_prompt
}
ZSH_THEME_GIT_PROMPT_PREFIX="%{$fg_bold[cyan]%}(%{$fg_bold[red]%}"
ZSH_THEME_GIT_PROMPT_SUFFIX="%{$reset_color%} "
ZSH_THEME_GIT_PROMPT_DIRTY=" %{$fg_bold[yellow]%}✗%{$fg_bold[cyan]%})"
ZSH_THEME_GIT_PROMPT_CLEAN="%{$fg_bold[cyan]%})"
__zsh_prompt
EOF
)"

# Add notice that Oh My Bash! has been removed from images and how to provide information on how to install manually
OMB_README="$(cat \
<<'EOF'
"Oh My Bash!" has been removed from this image in favor of a simple shell prompt. If you 
still wish to use it, remove "~/.oh-my-bash" and install it from: https://github.com/ohmybash/oh-my-bash
You may also want to consider "Bash-it" as an alternative: https://github.com/bash-it/bash-it
See here for infomation on adding it to your image or dotfiles: https://aka.ms/codespaces/omb-remove
EOF
)"
OMB_STUB="$(cat \
<<'EOF'
#!/usr/bin/env bash
if [ -t 1 ]; then
    cat $HOME/.oh-my-bash/README.md
fi
EOF
)"

# Add RC snippet and custom bash prompt
if [ "${RC_SNIPPET_ALREADY_ADDED}" != "true" ]; then
    echo "${RC_SNIPPET}" >> /etc/bash.bashrc
    echo "${CODESPACES_BASH}" >> "${USER_RC_PATH}/.bashrc"
    echo 'export PROMPT_DIRTRIM=4' >> "${USER_RC_PATH}/.bashrc"
    if [ "${USERNAME}" != "root" ]; then
        echo "${CODESPACES_BASH}" >> "/root/.bashrc"
        echo 'export PROMPT_DIRTRIM=4' >> "/root/.bashrc"
    fi
    chown ${USERNAME}:${USERNAME} "${USER_RC_PATH}/.bashrc"
    RC_SNIPPET_ALREADY_ADDED="true"
fi

# Add stub for Oh My Bash!
if [ ! -d "${USER_RC_PATH}/.oh-my-bash}" ] && [ "${INSTALL_OH_MYS}" = "true" ]; then
    mkdir -p "${USER_RC_PATH}/.oh-my-bash" "/root/.oh-my-bash"
    echo "${OMB_README}" >> "${USER_RC_PATH}/.oh-my-bash/README.md"
    echo "${OMB_STUB}" >> "${USER_RC_PATH}/.oh-my-bash/oh-my-bash.sh"
    chmod +x "${USER_RC_PATH}/.oh-my-bash/oh-my-bash.sh"
    if [ "${USERNAME}" != "root" ]; then
        echo "${OMB_README}" >> "/root/.oh-my-bash/README.md"
        echo "${OMB_STUB}" >> "/root/.oh-my-bash/oh-my-bash.sh"
        chmod +x "/root/.oh-my-bash/oh-my-bash.sh"
    fi
    chown -R "${USERNAME}:${USERNAME}" "${USER_RC_PATH}/.oh-my-bash"
fi

# Optionally install and configure zsh and Oh My Zsh!
if [ "${INSTALL_ZSH}" = "true" ]; then
    if ! type zsh > /dev/null 2>&1; then
        apt-get-update-if-needed
        apt-get install -y zsh
    fi
    if [ "${ZSH_ALREADY_INSTALLED}" != "true" ]; then
        echo "${RC_SNIPPET}" >> /etc/zsh/zshrc
        ZSH_ALREADY_INSTALLED="true"
    fi

    # Adapted, simplified inline Oh My Zsh! install steps that adds, defaults to a codespaces theme.
    # See https://github.com/ohmyzsh/ohmyzsh/blob/master/tools/install.sh for official script.
    OH_MY_INSTALL_DIR="${USER_RC_PATH}/.oh-my-zsh"
    if [ ! -d "${OH_MY_INSTALL_DIR}" ] && [ "${INSTALL_OH_MYS}" = "true" ]; then
        TEMPLATE_PATH="${OH_MY_INSTALL_DIR}/templates/zshrc.zsh-template"
        USER_RC_FILE="${USER_RC_PATH}/.zshrc"
        umask g-w,o-w
        mkdir -p ${OH_MY_INSTALL_DIR}
        git clone --depth=1 \
            -c core.eol=lf \
            -c core.autocrlf=false \
            -c fsck.zeroPaddedFilemode=ignore \
            -c fetch.fsck.zeroPaddedFilemode=ignore \
            -c receive.fsck.zeroPaddedFilemode=ignore \
            "https://github.com/ohmyzsh/ohmyzsh" "${OH_MY_INSTALL_DIR}" 2>&1
        echo -e "$(cat "${TEMPLATE_PATH}")\nDISABLE_AUTO_UPDATE=true\nDISABLE_UPDATE_PROMPT=true" > ${USER_RC_FILE}
        sed -i -e 's/ZSH_THEME=.*/ZSH_THEME="codespaces"/g' ${USER_RC_FILE}

        mkdir -p ${OH_MY_INSTALL_DIR}/custom/themes
        echo "${CODESPACES_ZSH}" > "${OH_MY_INSTALL_DIR}/custom/themes/codespaces.zsh-theme"
        # Shrink git while still enabling updates
        cd "${OH_MY_INSTALL_DIR}"
        git repack -a -d -f --depth=1 --window=1
        # Copy to non-root user if one is specified
        if [ "${USERNAME}" != "root" ]; then
            cp -rf "${USER_RC_FILE}" "${OH_MY_INSTALL_DIR}" /root
            chown -R ${USERNAME}:${USERNAME} "${USER_RC_PATH}"
        fi
    fi
fi

# Persist image metadata info, script if meta.env found in same directory
META_INFO_SCRIPT="$(cat << 'EOF'
#!/bin/sh
. /usr/local/etc/vscode-dev-containers/meta.env

# Minimal output
if [ "$1" = "version" ] || [ "$1" = "image-version" ]; then
    echo "${VERSION}"
    exit 0
elif [ "$1" = "release" ]; then
    echo "${GIT_REPOSITORY_RELEASE}"
    exit 0
elif [ "$1" = "content" ] || [ "$1" = "content-url" ] || [ "$1" = "contents" ] || [ "$1" = "contents-url" ]; then
    echo "${CONTENTS_URL}"
    exit 0
fi

#Full output
echo
echo "Development container image information"
echo
if [ ! -z "${VERSION}" ]; then echo "- Image version: ${VERSION}"; fi
if [ ! -z "${DEFINITION_ID}" ]; then echo "- Definition ID: ${DEFINITION_ID}"; fi
if [ ! -z "${VARIANT}" ]; then echo "- Variant: ${VARIANT}"; fi
if [ ! -z "${GIT_REPOSITORY}" ]; then echo "- Source code repository: ${GIT_REPOSITORY}"; fi
if [ ! -z "${GIT_REPOSITORY_RELEASE}" ]; then echo "- Source code release/branch: ${GIT_REPOSITORY_RELEASE}"; fi
if [ ! -z "${BUILD_TIMESTAMP}" ]; then echo "- Timestamp: ${BUILD_TIMESTAMP}"; fi
if [ ! -z "${CONTENTS_URL}" ]; then echo && echo "More info: ${CONTENTS_URL}"; fi
echo
EOF
)"
SCRIPT_DIR="$(cd $(dirname $0) && pwd)"
if [ -f "${SCRIPT_DIR}/meta.env" ]; then
    mkdir -p /usr/local/etc/vscode-dev-containers/
    cp -f "${SCRIPT_DIR}/meta.env" /usr/local/etc/vscode-dev-containers/meta.env
     echo "${META_INFO_SCRIPT}" > /usr/local/bin/devcontainer-info
    chmod +x /usr/local/bin/devcontainer-info
fi

# Write marker file
mkdir -p "$(dirname "${MARKER_FILE}")"
echo -e "\
    PACKAGES_ALREADY_INSTALLED=${PACKAGES_ALREADY_INSTALLED}\n\
    LOCALE_ALREADY_SET=${LOCALE_ALREADY_SET}\n\
    EXISTING_NON_ROOT_USER=${EXISTING_NON_ROOT_USER}\n\
    RC_SNIPPET_ALREADY_ADDED=${RC_SNIPPET_ALREADY_ADDED}\n\
    ZSH_ALREADY_INSTALLED=${ZSH_ALREADY_INSTALLED}" > "${MARKER_FILE}"

echo "Done!"
