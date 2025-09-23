# Dockerfiles

This includes dockerfiles to build Dapr release and debug images and development container images for go dev environment.

* Dockerfile: Dapr Release Image
* Dockerfile-debug: Dapr Debug Image - WIP
* Dockerfile-dev: Development container image for VS Code Remote-Containers and GitHub Codespaces.

## Dev Container

### Container build args

The Dev Container can be rebuilt with custom options. Relevant args (and their default values) include:

* `GOVERSION` (default: `1.24.6`)
* `INSTALL_ZSH` (default: `true`)
* `KUBECTL_VERSION` (default: `latest`)
* `HELM_VERSION` (default: `latest`)
* `MINIKUBE_VERSION` (default: `latest`)
* `DAPR_CLI_VERSION` (default: `latest`)
* `PROTOC_VERSION` (default: `25.4`)
* `PROTOC_GEN_GO_VERSION` (default: `1.32`)
* `PROTOC_GEN_GO_GRPC_VERSION` (default: `1.3`)
* `GOLANGCI_LINT_VERSION` (default: `1.45.2`)

### Setup multi-arch Docker builds

Building multi-arch Docker images requires using QEMU, which we do not distribute as part of the container image.

To configure Docker within the dev container for multi-arch builds, within the dev container run this command:

```sh
/usr/local/share/setup-docker-multiarch.sh
```
