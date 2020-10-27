# Developing Dapr

This section will walk you through on how to clone and build the Dapr runtime.
First, make sure you have [Go 1.15](https://golang.org/dl/) installed.


## Setup environment

This document helps you get started developing Dapr. If you find any problem while following this guide, please create a Pull Request to update this document.

### Docker environment

1. Install [Docker](https://docs.docker.com/install/)
    > For Linux, you'll have to configure docker to run without sudo for this to work, because of the environment variables.  See the following on how to configure [this](https://docs.docker.com/install/linux/linux-postinstall/).

2. Create your [Docker Hub account](https://hub.docker.com)


### Go dev environment

#### Linux and MacOS

1. The Go language environment [(instructions)](https://golang.org/doc/install#tarball).
   * Make sure that your GOPATH and PATH are configured correctly
   ```bash
   export GOPATH=~/go
   export PATH=$PATH:$GOPATH/bin
   ```
2. [Delve](https://github.com/go-delve/delve/tree/master/Documentation/installation) for Debugging

#### Windows

1. The Go language environment [(instructions)](https://golang.org/doc/install#windows).
   Make sure that your GOPATH and PATH are configured correctly - You may set environment variables through the "Environment Variables" button on the "Advanced" tab of the "System" control panel. Some versions of Windows provide this control panel through the "Advanced System Settings" option inside the "System" control panel.
   ```
   GOPATH=c:\go
   PATH=%GOPATH%\bin;...
   ```
2. [Delve](https://github.com/go-delve/delve/tree/master/Documentation/installation) for Debugging
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

### Kubernetes environment

1. [Setup Minikube for Local environment](https://docs.dapr.io/operations/hosting/kubernetes/cluster/setup-minikube/)
2. [Setup Azure Kubernetes Service](https://docs.dapr.io/operations/hosting/kubernetes/cluster/setup-aks/)
3. [Helm 3.x](https://helm.sh/docs/intro/install/)

### Installing Make

Dapr uses `make` to build and test its binaries.

#### Windows

Download [MingGW](https://sourceforge.net/projects/mingw/files/MinGW/Extension/make/mingw32-make-3.80-3/) and use `ming32-make.exe` instead of `make`.

Make sure `ming32-make.exe` is in your path.

#### Linux

```sudo apt-get install build-essential```

#### Mac

In Xcode preferences go to the "Downloads" tab and under "Components" push the "Install" button next to "Command Line Tools". After you have successfully downloaded and installed the command line tools you should also type the following command in the Terminal to make sure all your Xcode command line tools are switched to use the new versions:

```sudo xcode-select -switch /Applications/Xcode.app/Contents/Developer```

Once everything is successfully installed you should see make and other command line developer tools in /usr/bin.

## Cloning the repo

```bash
cd $GOPATH/src
mkdir -p github.com/dapr/dapr
git clone https://github.com/dapr/dapr.git github.com/dapr/dapr
```

## Build the Dapr binaries

You can build dapr binaries with the `make` tool.
When running `make`, you need to be at the root of the `dapr/dapr` repo directory, for example: `$GOPATH/src/github.com/dapr/dapr`.<br><br>
Once built, the release binaries will be found in `./dist/{os}_{arch}/release/`, where `{os}_{arch}` is your current OS and architecture.

For example, running `make build` on MacOS will generate the directory `./dist/darwin_amd64/release.

> Note : for a Windows environment with MinGW, use `mingw32-make.exe` instead of `make`.

* Build for your current local environment

```bash
cd $GOPATH/src/github.com/dapr/dapr/
make build
```

* Cross compile for multi platforms

```bash
make build GOOS=linux GOARCH=amd64
```

## Run unit tests

```bash
make test
```

## Debug Dapr

We highly recommend to use [VSCode with Go plugin](https://marketplace.visualstudio.com/items?itemName=ms-vscode.Go) for your productivity. If you want to use the different editors, you can find the [list of editor plugins](https://github.com/go-delve/delve/blob/master/Documentation/EditorIntegration.md) for Delve.

This section introduces how to start debugging with Delve CLI. Please see [Delve documentation](https://github.com/go-delve/delve/tree/master/Documentation) for the detail usage.

### Start the dapr runtime with a debugger

```bash
$ cd $GOPATH/src/github.com/dapr/dapr/cmd/daprd
$ dlv debug .
Type 'help' for list of commands.
(dlv) break main.main
(dlv) continue
```

### Attach a Debugger to running process

This is useful to debug dapr when the process is running.

1. Build dapr binaries for debugging
   With `DEBUG=1` option, dapr binaries will be generated without code optimization in `./dist/{os}_{arch}/debug/`

```bash
$ make DEBUG=1 build
```

2. Create component yaml file under `./dist/{os}_{arch}/debug/components` e.g. statstore component yaml
3. Run dapr runtime

```bash
$ /dist/{os}_{arch}/debug/daprd
```

4. Find the process id and attach the debugger

```bash
$ dlv attach [pid]
```

### Debug unit-tests

```bash
# Specify the package that you want to test
# e.g. debugging ./pkg/actors
$ dlv test ./pkg/actors
```

## Developing on Kubernetes environment

### Setting environment variable

* **DAPR_REGISTRY** : should be set to docker.io/<your_docker_hub_account>.
* **DAPR_TAG** : should be set to whatever value you wish to use for a container image tag.

**Linux/macOS**

```
export DAPR_REGISTRY=docker.io/<your_docker_hub_account>
export DAPR_TAG=dev
```

**Windows**

```
set DAPR_REGISTRY=docker.io/<your_docker_hub_account>
set DAPR_TAG=dev
```

### Building the Container Image

Run the appropriate command below to build the container image.

**Linux/macOS**
```
# Build Linux binaries
make build-linux

# Build Docker image with Linux binaries
make docker-build
```

**Windows**
```
# Build Linux binaries
mingw32-make build-linux

# Build Docker image with Linux binaries
mingw32-make.exe docker-build
```

## Push the Container Image

To push the image to DockerHub, run:

**Linux/macOS**
```
make docker-push
```

**Windows**
```
mingw32-make.exe docker-push
```

## Deploy Dapr With Your Changes

Now we'll deploy Dapr with your changes.

Create the dapr-system namespace

```
kubectl create namespace dapr-system
```

If you deployed Dapr to your cluster before, delete it now using:

```
helm uninstall dapr -n dapr-system
```

and run the following to deploy your change to your Kubernetes cluster:

**Linux/macOS**
```
make docker-deploy-k8s
```

**Windows**
```
mingw32-make.exe docker-deploy-k8s
```

## Verifying your changes

Once Dapr is deployed, print the Dapr pods:

```
kubectl get pod -n dapr-system

NAME                                    READY   STATUS    RESTARTS   AGE
dapr-operator-86cddcfcb7-v2zjp          1/1     Running   0          4d3h
dapr-placement-5d6465f8d5-pz2qt         1/1     Running   0          4d3h
dapr-sidecar-injector-dc489d7bc-k2h4q   1/1     Running   0          4d3h
```
