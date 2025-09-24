# Setup Dapr development environment

This document helps you get started developing Dapr. If you find any problems while following this guide, please create a Pull Request to update this document.

## Git

1. Install [Git](https://git-scm.com/downloads)

   > On Windows, the Dapr build environment depends on Git BASH that comes as a part of [Git for Windows](https://gitforwindows.org).
   >
   > Ensure that the Git and Unix tools are part of the `PATH` environment variable, and that the editor experience is integrated with the Windows Terminal. For example, if [installing Git with chocolatey](https://chocolatey.org/packages/git), you may want to specify:
   >
   > ```cmd
   > choco install git -y --package-parameters="/GitAndUnixToolsOnPath /WindowsTerminal"
   > ```

## Docker environment

1. Install [Docker](https://docs.docker.com/install/)
    > For Linux, you'll have to configure docker to run without `sudo` for the dapr build scripts to work. Follow the instructions to [manage Docker as a non-root user](https://docs.docker.com/engine/install/linux-postinstall/#manage-docker-as-a-non-root-user).

2. Create your [Docker Hub account](https://hub.docker.com/signup) if you don't already have one.

## Go (Golang)

1. Download and install [Go 1.24.6 or later](https://golang.org/doc/install#tarball).

2. Install [Delve](https://github.com/go-delve/delve/tree/master/Documentation/installation) for Go debugging, if desired.

3. Install [golangci-lint](https://golangci-lint.run/usage/install) version 1.64.6.

## Setup a Kubernetes development environment

1. Follow the guide on [how to set up a Kubernetes cluster for Dapr](https://docs.dapr.io/operations/hosting/kubernetes/cluster/).

2. For development purposes, you will also want to follow the optional steps to install [Helm 3.x](https://helm.sh/docs/intro/install/).

## Installing Make

Dapr uses `make` for a variety of build and test actions, and needs to be installed as appropriate for your platform:

### Linux

1. Install the `build-essential` package:

   ```bash
   sudo apt-get install build-essential
   ```

### macOS

1. Ensure that build tools are installed:

   ```sh
   xcode-select --install
   ```

2. When completed, you should see `make` and other command line developer tools in `/usr/bin`.

### Windows

1. Install MinGW and make with [Chocolatey](https://chocolatey.org/install):

   ```cmd
   choco install mingw
   choco install make
   ```
