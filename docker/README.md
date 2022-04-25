# Dockerfiles

This includes dockerfiles to build Dapr release and debug images and development container images for go dev environment.

* Dockerfile: Dapr Release Image
* Dockerfile-debug: Dapr Debug Image - WIP
* Dockerfile-dev: Development container image for VS Code Remote-Containers and GitHub Codespaces.

## Dev Container

### Container build args

The Dev Container can be rebuilt with custom options. Relevant args (and their default values) include:

* `GOVERSION` (default: `1.17`)
* `INSTALL_ZSH` (default: `true`)
* `KUBECTL_VERSION` (default: `latest`)
* `HELM_VERSION` (default: `latest`)
* `MINIKUBE_VERSION` (default: `latest`)
* `DAPR_CLI_VERSION` (default: `latest`)
* `PROTOC_VERSION` (default: `3.14.0"`)

### Setup multi-arch Docker builds

Building multi-arch Docker images requires using QEMU, which we do not distribute as part of the container image.

To configure Docker within the dev container for multi-arch builds, within the dev container run this command:

```sh
/usr/local/share/setup-docker-multiarch.sh
```
