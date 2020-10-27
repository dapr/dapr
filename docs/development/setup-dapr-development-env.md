# Setup Dapr development environment

This document helps you get started developing Dapr. If you find any problem while following this guide, please create a Pull Request to update this document.

## Docker environment

1. Install [Docker](https://docs.docker.com/install/)
    > For Linux, you'll have to configure docker to run without sudo for this to work, because of the environment variables.  See the following on how to configure [this](https://docs.docker.com/install/linux/linux-postinstall/).

2. Create your [Docker Hub account](https://hub.docker.com)


## Golang dev environment

### Linux and MacOS

1. Go 1.15 [(instructions)](https://golang.org/doc/install#tarball).
   * Make sure that your GOPATH and PATH are configured correctly
   ```bash
   export GOPATH=~/go
   export PATH=$PATH:$GOPATH/bin
   ```
2. [Delve](https://github.com/go-delve/delve/tree/master/Documentation/installation) for Debugging

### Windows

1. The Go language environment [(instructions)](https://golang.org/doc/install#windows).
   Make sure that your GOPATH and PATH are configured correctly - You may set environment variables through the "Environment Variables" button on the "Advanced" tab of the "System" control panel. Some versions of Windows provide this control panel through the "Advanced System Settings" option inside the "System" control panel.
   ```
   GOPATH=c:\go
   PATH=%GOPATH%\bin;...
   ```
2. [Delve](https://github.com/go-delve/delve/tree/master/Documentation/installation) for Go debugging
    Delve for Go (NOTE: this is NOT Delve from the Office365) is a debugger utility is a debugger for the Go programming language.  Please refer to [Delve Home Page](https://github.com/go-delve/delve) for a detailed explaination.
3. [Git for Windows](https://gitforwindows.org)
   * Install [Git with chocolatey](https://chocolatey.org/packages/git) and ensure that Git bin directory is in PATH environment variable
    ```bash
    choco install git -y --package-parameters="/GitAndUnixToolsOnPath /WindowsTerminal /NoShellIntegration"
    ```
4. [MinGW](http://www.mingw.org/)
  Install [MinGW with chocolatey](https://chocolatey.org/packages/mingw) and ensure that MinGW bin directory is in PATH environment variable

    ```bash
    choco install mingw
    ```

## Setup Kubernetes development environment

For development environment, you must install helm client and tiller.

1. [Setup Minikube for Local environment](https://docs.dapr.io/operations/hosting/kubernetes/cluster/setup-minikube/)
2. [Setup Azure Kubernetes Service](hhttps://docs.dapr.io/operations/hosting/kubernetes/cluster/setup-aks/)
