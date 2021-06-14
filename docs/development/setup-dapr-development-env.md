# Setup Dapr development environment

This document helps you get started developing Dapr. If you find any problems while following this guide, please create a Pull Request to update this document.

## Git

1. Install [Git](https://git-scm.com/downloads)

   > On Windows, the Dapr build environment depends on Git BASH that comes as a part of [Git for Windows](https://gitforwindows.org).
   >
   > Ensure that the Git and Unix tools are part of the `PATH` environment variable, and that the editor experience is integrated with Windows terminal. For example, if [installing Git with chocolatey](https://chocolatey.org/packages/git), you may want to specify:
   >
   > ```cmd
   > choco install git -y --package-parameters="/GitAndUnixToolsOnPath /WindowsTerminal"
   > ```

## Docker environment

1. Install [Docker](https://docs.docker.com/install/)
    > For Linux, you'll have to configure docker to run without `sudo` for the dapr build scripts to work. Follow the instructions to [manage Docker as a non-root user](https://docs.docker.com/engine/install/linux-postinstall/#manage-docker-as-a-non-root-user).

2. Create your [Docker Hub account](https://hub.docker.com/signup) if you don't already have one.

## Golang dev environment

1. Download and install [Go 1.16 or later](https://golang.org/doc/install#tarball).

   - Make sure that your `GOPATH` and `PATH` environment variables are configured so that go modules can be run. For example:

     **Linux and MacOS**

     ```bash
     export GOPATH=~/go
     export PATH=$PATH:$GOPATH/bin
     ```

     **Windows**

     ```cmd
     set GOPATH=%USERPROFILE%\go
     set PATH=%PATH%;%GOPATH%\bin
     ```

2. Install [Delve](https://github.com/go-delve/delve/tree/master/Documentation/installation) for Go debugging, if desired.

3. Install [golangci-lint](https://golangci-lint.run/usage/install).

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

### MacOS

1. In Xcode Preferences, go to the _Downloads_ tab.
2. Under _Components_ push the _Install_ button next to _Command Line Tools_.
3. After the tools are installed, ensure that the tools are switched to the new versions:

   ```bash
   sudo xcode-select -switch /Applications/Xcode.app/Contents/Developer
   ```

4. When completed, you should see `make` and other command line developer tools in `/usr/bin`.

### Windows

1. Install [MinGW v8.1.0](https://chocolatey.org/packages/mingw/8.1.0) with [Chocolatey](https://chocolatey.org/install):

   ```cmd
   choco install mingw --version 8.1.0
   ```

   > Versions of MinGW newer than 8.1.0 will not contain `mingw32-make.exe`, so the `--version` must be specified.

2. Create a link to `mingw32-make.exe` so that it can be invoked as `make`:

   ```cmd
   mklink C:\ProgramData\chocolatey\bin\make.exe C:\ProgramData\chocolatey\bin\mingw32-make.exe
   ```
