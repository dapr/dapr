# Setup Dapr development environment using VS Code

This document helps you get started developing Dapr using VSCode. If you find any problem while following this guide, please create a Pull Request to update this document.

## VSCode Remote - Container environment

If using [VS Code](https://code.visualstudio.com/), you can develop Dapr from a [Remote - Container](https://code.visualstudio.com/docs/remote/containers) without installing go tools and helm on your local machine. If you're working in windows, you do not need to install mingw tools.

### Prerequisites

1. [Docker](https://www.docker.com/products/developer-tools)
   - For Windows user, ensure that you share your local drive in docker desktop by following [this doc](https://code.visualstudio.com/docs/remote/containers#_installation)
2. VS Code extension - [Remote - Containers](https://marketplace.visualstudio.com/items?itemName=ms-vscode-remote.remote-containers)
3. Kubernetes environment without helm install
   - [Setup Minikube for Local environment](https://docs.dapr.io/operations/hosting/kubernetes/cluster/setup-minikube/)
   - [Setup Azure Kubernetes Service](https://docs.dapr.io/operations/hosting/kubernetes/cluster/setup-aks/)

> Note: by default, [devcontainer configuration](../../.devcontainer/devcontainer.json) mounts `~/.kube` and `~/.minikube` directory to the container. Comment out mounting options based on your environment. Otherwise, you will face the mounting error.

```json
"runArgs": [
	...
	// Comment out if you do not use kubectl inside container
	"--mount", "type=bind,source=${env:HOME}${env:USERPROFILE}/.kube,target=/home/dapr/.kube-localhost",
	// Comment out if you do not use minikube inside container
	"--mount", "type=bind,source=${env:HOME}${env:USERPROFILE}/.minikube,target=/home/dapr/.minikube-localhost",
	...
]
```

### Setup

1. Clone the repository:
```bash
git clone https://github.com/dapr/dapr.git
```

2. Open the repository in VS Code
```bash
cd dapr
code .
```

3. Open the workspace in the remote container (either when prompted or via the command palette and the `Remote-Containers: Reopen in Container` command)

### Developing Dapr

1. Once container is loaded, open bash terminal on VSCode
2. See [Developing Dapr](./developing-dapr.md) to develop Dapr

### Use your own container image

The [default remote-container config](../../.devcontainer/devcontainer.json) uses the default image in [daprio dockerhub](https://hub.docker.com/r/daprio/dapr-dev). This section describes how to use your own development image.

1. Edit [docker/Dockerfile-dev](../../docker/Dockerfile-dev)
2. Uncomment `"dockerFile"` in [devcontainer.json](../../.devcontainer/devcontainer.json) to use container built with [docker/Dockerfile-dev](../../docker/Dockerfile-dev).

```json
{
	"name": "Dapr Dev Environment",
	// Update container version when you update dev-container
	// "image": "docker.io/daprio/dapr-dev:0.1.0",
	// Uncomment if you want to use your local docker image
	"dockerFile": "../docker/Dockerfile-dev",
	"runArgs": [
...
```

3. Reopen the workspace in the remote container (either when prompted or via the command palette and the `Remote-Containers: Reopen in Container` command)
