# Introduction

This chart deploys the Dapr control plane system services on a Kubernetes cluster using the Helm package manager.

## Chart Details

This chart installs Dapr via "child-charts":

* Dapr Component and Configuration Kubernetes CRDs
* Dapr Operator
* Dapr Sidecar injector
* Dapr Sentry
* Dapr Placement
* Dapr Dashboard

## Prerequisites

* Kubernetes cluster with RBAC (Role-Based Access Control) enabled is required
* Helm 3.0.2 or newer

## Resources Required
The chart deploys pods that consume minimum resources as specified in the resources configuration parameter.

## Install the Chart

Ensure Helm is initialized in your Kubernetes cluster.

For more details on initializing Helm, [read the Helm docs](https://helm.sh/docs/)

1. Add dapr.github.io as an helm repo
    ```
    helm repo add dapr https://dapr.github.io/helm-charts/
    helm repo update
    ```

2. Install the Dapr chart on your cluster in the dapr-system namespace:
    ```
    helm install dapr dapr/dapr --namespace dapr-system
    ``` 

## Verify installation

Once the chart is installed, verify the Dapr control plane system service pods are running in the `dapr-system` namespace:
```
kubectl get pods --namespace dapr-system
```

## Uninstall the Chart

To uninstall/delete the `dapr` release:
```
helm uninstall dapr -n dapr-system
```

## Upgrade the charts

*Before* upgrading Dapr, make sure you have exported the existing certs. Follow the upgrade HowTo instructions in [Upgrading Dapr with Helm](https://github.com/dapr/docs/blob/master/howto/deploy-k8s-prod/README.md#upgrading-dapr-with-helm).

## Configuration
The Helm chart has the follow configuration options that can be supplied:

| Parameter                                 | Description                                                             | Default                 |
|-------------------------------------------|-------------------------------------------------------------------------|-------------------------|
| `global.registry`                         | Global Dapr docker image registry                                       | `docker.io/daprio`      |
| `global.tag`                              | Global Dapr docker image version tag                                    | `0.11.0`                |
| `global.logAsJson`                        | Json log format for control plane services                              | `false`                 |
| `global.imagePullPolicy`                  | Global Control plane service imagePullPolicy                            | `Always`                |
| `global.imagePullSecret`                  | Control plane service image pull secret for docker registry             | `""`                    |
| `global.ha.enabled`                       | High Availability mode enabled for control plane service                | `false`                 |
| `global.ha.replicaCount`                  | Number of replicas of control plane services in High Availability mode  | `3`                     |
| `global.prometheus.enabled`               | Prometheus metrics enablement for control plane services                | `true`                  |
| `global.prometheus.port`                  | Prometheus scrape http endpoint port                                    | `9090`                  |
| `global.mtls.enabled`                     | Mutual TLS enablement                                                   | `true`                  |
| `global.mtls.workloadCertTTL`             | TTL for workload cert                                                   | `24h`                   |
| `global.mtls.allowedClockSkew`            | Allowed clock skew for workload cert rotation                           | `15m`                   |
| `global.daprControlPlaneOs`               | Operating System for Dapr control plane                                 | `linux`                 |
| `dapr_operator.replicaCount`              | Number of replicas for Operator                                         | `1`                     |
| `dapr_operator.logLevel`                  | Operator Log level                                                      | `info`                  |
| `dapr_operator.image.name`                | Operator docker image name (`global.registry/dapr_operator.image.name`) | `dapr`                  |
| `dapr_sidecar_injector.replicaCount`      | Number of replicas for Sidecar Injector                                 | `1`                     |
| `dapr_sidecar_injector.logLevel`          | Sidecar Injector Log level                                              | `info`                  |
| `dapr_sidecar_injector.image.name`        | Dapr runtime sidecar image name injecting to application (`global.registry/dapr_sidecar_injector.image.name`) | `daprd`                 |
| `dapr_sentry.replicaCount`                | Number of replicas for Sentry CA                                        | `1`                     |
| `dapr_sentry.logLevel`                    | Sentry CA Log level                                                     | `info`                  |
| `dapr_sentry.image.name`                  | Sentry CA docker image name (`global.registry/dapr_sentry.image.name`)  | `dapr`                  |
| `dapr_sentry.tls.issuer.certPEM`          | Issuer Certificate cert                                                 | `""`                    |
| `dapr_sentry.tls.issuer.keyPEM`           | Issuer Private Key cert                                                 | `""`                    |
| `dapr_sentry.tls.root.certPEM`            | Root Certificate cert                                                   | `""`                    |
| `dapr_sentry.trustDomain`                 | Trust domain (logical group to manage app trust relationship) for access control list | `cluster.local`  |
| `dapr_placement.replicaCount`             | Number of replicas for Dapr Placement                                   | `1`                     |
| `dapr_placement.logLevel`                 | Dapr Placement service Log level                                        | `info`                  |
| `dapr_placement.image.name`               | Dapr Placement service docker image name (`global.registry/dapr_placement.image.name`) | `dapr`   |
| `dapr_dashboard.replicaCount`             | Number of replicas for Dapr Dashboard                                   | `1`                     |
| `dapr_dashboard.logLevel`                 | Dapr Dashboard service Log level                                        | `info`                  |
| `dapr_dashboard.image.registry`           | Dapr Dashboard docker registry                                          | `docker.io/daprio`      |
| `dapr_dashboard.image.name`               | Dapr Dashboard docker image name                                        | `dashboard`             |
| `dapr_dashboard.image.tag`                | Dapr Dashboard docker image tag                                         | `"0.2.0"`               |
