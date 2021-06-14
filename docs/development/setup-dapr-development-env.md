# Setup Dapr development environment

This document helps you get started developing Dapr. If you find any problems while following this guide, please create a Pull Request to update this document.

## Docker environment

1. Install [Docker](https://docs.docker.com/install/)
    > For Linux, you'll have to configure docker to run without `sudo` for the dapr build scripts to work. Follow the instructions to [manage Docker as a non-root user](https://docs.docker.com/engine/install/linux-postinstall/#manage-docker-as-a-non-root-user).

2. Create your [Docker Hub account](https://hub.docker.com/signup) if you don't already have one.

## Golang dev environment

### Linux and MacOS

1. Download and install [Go 1.16 or later](https://golang.org/doc/install#tarball).
   - Make sure that your `GOPATH` and `PATH` environment variables are configured so that go modules can be run. For example:

   ```bash
   export GOPATH=~/go
   export PATH=$PATH:$GOPATH/bin
   ```

2. Install [Delve](https://github.com/go-delve/delve/tree/master/Documentation/installation) for Go debugging, if desired.

3. Install [golangci-lint](https://golangci-lint.run/usage/install).

### Windows

1. Download and install [Go 1.16 or later](https://golang.org/doc/install#windows).
   - Make sure that your `GOPATH` and `PATH` environment variables are configured so that go modules can be run. For example:

   ```cmd
   set GOPATH=%USERPROFILE%\go
   set PATH=%PATH%;%GOPATH%\bin
   ```

   - You may also set environment variables through the "Environment Variables" button on the "Advanced" tab of the "System" control panel. Some versions of Windows provide this control panel through the "Advanced System Settings" option inside the "System" control panel.

2. Install [Delve](https://github.com/go-delve/delve/tree/master/Documentation/installation) for Go debugging, if desired.
   Delve for Go (NOTE: this is NOT Delve from the Office365) is a debugger utility is a debugger for the Go programming language. Please refer to [Delve Home Page](https://github.com/go-delve/delve) for a detailed explanation.

3. [Git for Windows](https://gitforwindows.org)
   - Install [Git with chocolatey](https://chocolatey.org/packages/git) and ensure that Git bin directory is in PATH environment variable

    ```cmd
    choco install git -y --package-parameters="/GitAndUnixToolsOnPath /WindowsTerminal /NoShellIntegration"
    ```

4. [MinGW](https://sourceforge.net/projects/mingw/files/MinGW)
   Install [MinGW v8.1.0 with chocolatey](https://chocolatey.org/packages/mingw/8.1.0) and ensure that MinGW bin directory is in PATH environment variable. Version 8.1.0 is the last version that installs mingw32-make.exe.

    ```cmd
    choco install mingw --version 8.1.0
    ```

5. Rename mingw32-make.exe to make.exe from an administrative PowerShell.

    ```cmd
    copy C:\ProgramData\chocolatey\lib\mingw\tools\install\mingw64\bin\mingw32-make.exe C:\ProgramData\chocolatey\lib\mingw\tools\install\mingw64\bin\make.exe
    ```

## Setup a Kubernetes development environment

Follow the guide on [how to set up a Kubernetes cluster for Dapr](https://docs.dapr.io/operations/hosting/kubernetes/cluster/).

For development purposes, you will also want to follow the optional steps to install [Helm 3.x](https://helm.sh/docs/intro/install/).

## Installing Make

Dapr uses `make` to build and test its binaries.

### Windows

Download [MingGW](https://sourceforge.net/projects/mingw/files/MinGW/Extension/make/mingw32-make-3.80-3/) and use `ming32-make.exe` instead of `make`.

Make sure `ming32-make.exe` is in your path.

### Linux

```bash
sudo apt-get install build-essential
```

### Mac

In Xcode preferences go to the "Downloads" tab and under "Components" push the "Install" button next to "Command Line Tools". After you have successfully downloaded and installed the command line tools you should also type the following command in the Terminal to make sure all your Xcode command line tools are switched to use the new versions:

```bash
sudo xcode-select -switch /Applications/Xcode.app/Contents/Developer
```

Once everything is successfully installed you should see `make` and other command line developer tools in `/usr/bin`.
