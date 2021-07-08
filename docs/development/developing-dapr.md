# Developing Dapr

## Setup Dapr development environment

There are several options for getting an environment up and running for Dapr development:

- Use a [GitHub Codespace](https://docs.dapr.io/contributing/codespaces/) configured for Dapr development. \[Requires [Beta sign-up](https://github.com/features/codespaces/signup)\]
- If you are using [Visual Studio Code](https://code.visualstudio.com/), you can [connect to a development container](./setup-dapr-development-using-vscode.md) configured for Dapr development.
- [Manually install](./setup-dapr-development-env.md) the necessary tools and frameworks for developing Dapr on your device.

## Cloning the repo

```bash
cd $GOPATH/src
mkdir -p github.com/dapr/dapr
git clone https://github.com/dapr/dapr.git github.com/dapr/dapr
```

## Build the Dapr binaries

You can build Dapr binaries with the `make` tool.

> On Windows, the `make` commands must be run under [git-bash](https://www.atlassian.com/git/tutorials/git-bash).
>
> These instructions also require that a `make` alias has been created for `mingw32-make.exe` according to the [setup instructions](./setup-dapr-development-env.md#installing-make).

- When running `make`, you need to be at the root of the `dapr/dapr` repo directory, for example: `$GOPATH/src/github.com/dapr/dapr`.

- Once built, the release binaries will be found in `./dist/{os}_{arch}/release/`, where `{os}_{arch}` is your current OS and architecture.

  For example, running `make build` on an Intel-based MacOS will generate the directory `./dist/darwin_amd64/release`.

- To build for your current local environment:

   ```bash
   cd $GOPATH/src/github.com/dapr/dapr/
   make build
   ```

- To cross-compile for a different platform:

   ```bash
   make build GOOS=windows GOARCH=amd64
   ```

  For example, developers on Windows who prefer to develop in [WSL2](https://docs.microsoft.com/en-us/windows/wsl/install-win10) can use the Linux development environment to cross-compile binaries like `daprd.exe` that run on Windows natively.

## Run unit tests

```bash
make test
```

## Debug Dapr

We highly recommend using VSCode with the [Go plugin](https://marketplace.visualstudio.com/items?itemName=ms-vscode.Go) for your productivity. If you want to use other code editors, please refer to the list of [editor plugins for Delve](https://github.com/go-delve/delve/blob/master/Documentation/EditorIntegration.md).

This section introduces how to start debugging with the Delve CLI. Please refer to the [Delve documentation](https://github.com/go-delve/delve/tree/master/Documentation) for more details.

### Start the Dapr runtime with a debugger

```bash
$ cd $GOPATH/src/github.com/dapr/dapr/cmd/daprd
$ dlv debug .
Type 'help' for list of commands.
(dlv) break main.main
(dlv) continue
```

### Attach a Debugger to running process

This is useful to debug Dapr when the process is running.

1. Build Dapr binaries for debugging.

   Use the `DEBUG=1` option to generate Dapr binaries without code optimization in `./dist/{os}_{arch}/debug/`

   ```bash
   make DEBUG=1 build
   ```

2. Create a component yaml file under `./dist/{os}_{arch}/debug/components` e.g. statestore component yaml

3. Start the Dapr runtime

   ```bash
   /dist/{os}_{arch}/debug/daprd
   ```

4. Find the process ID (e.g. `PID` displayed by the `ps` command for `daprd`) and attach the debugger

   ```bash
   dlv attach {PID}
   ```

### Debug unit-tests

Specify the package that you want to test when running the `dlv test`. For example, to debug the `./pkg/actors` tests:

```bash
dlv test ./pkg/actors
```

## Developing on Kubernetes environment

### Setting environment variable

- **DAPR_REGISTRY** : should be set to docker.io/<your_docker_hub_account>.
- **DAPR_TAG** : should be set to whatever value you wish to use for a container image tag.

#### Linux/macOS

```bash
export DAPR_REGISTRY=docker.io/<your_docker_hub_account>
export DAPR_TAG=dev
```

#### Windows

```cmd
set DAPR_REGISTRY=docker.io/<your_docker_hub_account>
set DAPR_TAG=dev
```

### Building the Container Image

```bash
# Build Linux binaries
make build-linux

# Build Docker image with Linux binaries
make docker-build
```

## Push the Container Image

To push the image to DockerHub, complete your `docker login` and run:

```bash
make docker-push
```

## Deploy Dapr With Your Changes

Now we'll deploy Dapr with your changes.

To create the dapr-system namespace:

```bash
kubectl create namespace dapr-system
```

If you deployed Dapr to your cluster before, delete it now using:

```bash
helm uninstall dapr -n dapr-system
```

To deploy your changes to your Kubernetes cluster:

```bash
make docker-deploy-k8s
```

## Verifying your changes

Once Dapr is deployed, print the Dapr pods:

```bash
$ kubectl get pod -n dapr-system

NAME                                    READY   STATUS    RESTARTS   AGE
dapr-operator-86cddcfcb7-v2zjp          1/1     Running   0          4d3h
dapr-placement-5d6465f8d5-pz2qt         1/1     Running   0          4d3h
dapr-sidecar-injector-dc489d7bc-k2h4q   1/1     Running   0          4d3h
```

## Debug Dapr in a Kubernetes Deployment

Refer to the [Dapr Docs](https://docs.dapr.io/developing-applications/debugging/debug-k8s/) on how to:

- [Debug the Dapr control plane on Kubernetes](https://docs.dapr.io/developing-applications/debugging/debug-k8s/debug-dapr-services/)
- [Debug the Dapr sidecar (daprd) on Kubernetes](https://docs.dapr.io/developing-applications/debugging/debug-k8s/debug-daprd/)
