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
* Helm 3.4.0 or newer

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
    helm install dapr dapr/dapr --namespace dapr-system --wait
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

Follow the upgrade HowTo instructions in [Upgrading Dapr with Helm](https://docs.dapr.io/operations/hosting/kubernetes/kubernetes-production/#upgrading-dapr-with-helm).


## Resource configuration
By default, all deployments are configured with blank `resources` attributes, which means that pods will consume as much cpu and memory as they want. This is probably fine for a local development or a non-production setup, but for production you should configure them. Consult Dapr docs and [Kubernetes docs](https://kubernetes.io/docs/concepts/configuration/manage-resources-containers/) for guidance on setting these values.

For example, in order to configure the `memory.requests` setting for the `dapr-operator` deployment, configure a values.yml file with the following:
```yaml
dapr_operator:
  resources:
    requests:
      memory: 200Mi
```

## Configuration

The Helm chart has the follow configuration options that can be supplied:

### Global options:
| Parameter                                 | Description                                                             | Default                 |
|-------------------------------------------|-------------------------------------------------------------------------|-------------------------|
| `global.registry`                         | Docker image registry                                                   | `docker.io/daprio`      |
| `global.tag`                              | Docker image version tag                                                | `1.5.0`            |
| `global.logAsJson`                        | Json log format for control plane services                              | `false`                 |
| `global.imagePullPolicy`                  | Global Control plane service imagePullPolicy                            | `IfNotPresent`          |
| `global.imagePullSecrets`                 | Control plane service images pull secrets for docker registry            | `""`                   |
| `global.ha.enabled`                       | Highly Availability mode enabled for control plane, except for placement service | `false`        |
| `global.ha.replicaCount`                  | Number of replicas of control plane services in Highly Availability mode  | `3`                   |
| `global.ha.disruption.minimumAvailable`   | Minimum amount of available instances for control plane. This can either be effective count or %. | ``             |
| `global.ha.disruption.maximumUnavailable`   | Maximum amount of instances that are allowed to be unavailable for control plane. This can either be effective count or %. | `25%`             |
| `global.prometheus.enabled`               | Prometheus metrics enablement for control plane services                | `true`                  |
| `global.prometheus.port`                  | Prometheus scrape http endpoint port                                    | `9090`                  |
| `global.mtls.enabled`                     | Mutual TLS enablement                                                   | `true`                  |
| `global.mtls.workloadCertTTL`             | TTL for workload cert                                                   | `24h`                   |
| `global.mtls.allowedClockSkew`            | Allowed clock skew for workload cert rotation                           | `15m`                   |
| `global.dnsSuffix`                        | Kuberentes DNS suffix                                                   | `.cluster.local`        |
| `global.daprControlPlaneOs`               | Operating System for Dapr control plane                                 | `linux`                 |
| `global.daprControlPlaneArch`             | CPU Architecture for Dapr control plane                                 | `amd64`                 |
| `global.nodeSelector`                     | Pods will be scheduled onto a node node whose labels match the nodeSelector | `{}`                 |
| `global.tolerations`                     | Pods will be allowed to schedule onto a node whose taints match the tolerations | `{}`                 |
| `global.labels`                           | Custom pod levels                                                       | `{}`                 |

### Dapr Dashboard options:
| Parameter                                 | Description                                                             | Default                 |
|-------------------------------------------|-------------------------------------------------------------------------|-------------------------|
| `dapr_dashboard.replicaCount`             | Number of replicas                                 | `1`                     |
| `dapr_dashboard.logLevel`                 | service Log level                                        | `info`                  |
| `dapr_dashboard.image.registry`           | docker registry                                          | `docker.io/daprio`      |
| `dapr_dashboard.image.imagePullSecrets`   | docker images pull secrets for docker registry           | `docker.io/daprio`      |
| `dapr_dashboard.image.name`               | docker image name                                        | `dashboard`             |
| `dapr_dashboard.image.tag`                | docker image tag                                         | `"0.6.0"`               |
| `dapr_dashboard.serviceType`              | Type of [Kubernetes service](https://kubernetes.io/docs/concepts/services-networking/service/#publishing-services-service-types) to use for the Dapr Dashboard service | `ClusterIP` |
| `dapr_dashboard.runAsNonRoot`             | Boolean value for `securityContext.runAsNonRoot`. You may have to set this to `false` when running in Minikube | `true` |
| `dapr_dashboard.resources`                | Value of `resources` attribute. Can be used to set memory/cpu resources/limits. See the section "Resource configuration" above. Defaults to empty | `{}` |

### Dapr Operator options:
| Parameter                                 | Description                                                             | Default                 |
|-------------------------------------------|-------------------------------------------------------------------------|-------------------------|
| `dapr_operator.replicaCount`              | Number of replicas                                                      | `1`                     |
| `dapr_operator.logLevel`                  | Log level                                                               | `info`                  |
| `dapr_operator.image.name`                | Docker image name (`global.registry/dapr_operator.image.name`)          | `dapr`                  |
| `dapr_operator.runAsNonRoot`              | Boolean value for `securityContext.runAsNonRoot`. You may have to set this to `false` when running in Minikube | `true` |
| `dapr_operator.resources`                 | Value of `resources` attribute. Can be used to set memory/cpu resources/limits. See the section "Resource configuration" above. Defaults to empty | `{}` |
| `dapr_operator.debug.enabled`             | Boolean value for enabling debug mode | `{}` |

### Dapr Placement options:
| Parameter                                 | Description                                                             | Default                 |
|-------------------------------------------|-------------------------------------------------------------------------|-------------------------|
| `dapr_placement.replicaCount`             | Number of replicas                                                      | `1`                     |
| `dapr_placement.replicationFactor`        | Number of consistent hashing virtual node | `100`   |
| `dapr_placement.logLevel`                 | Service Log level                                                       | `info`                  |
| `dapr_placement.image.name`               | Service docker image name (`global.registry/dapr_placement.image.name`) | `dapr`   |
| `dapr_placement.cluster.forceInMemoryLog` | Use in-memeory log store and disable volume attach when `global.ha.enabled` is true | `false`   |
| `dapr_placement.cluster.logStorePath`     | Mount path for persistent volume for log store in unix-like system when `global.ha.enabled` is true | `/var/run/dapr/raft-log`   |
| `dapr_placement.cluster.logStoreWinPath`  | Mount path for persistent volume for log store in windows when `global.ha.enabled` is true | `C:\\raft-log`   |
| `dapr_placement.volumeclaims.storageSize` | Attached volume size | `1Gi`   |
| `dapr_placement.volumeclaims.storageClassName` | storage class name |    |
| `dapr_placement.runAsNonRoot`             | Boolean value for `securityContext.runAsNonRoot`. Does not apply unless `forceInMemoryLog` is set to `true`. You may have to set this to `false` when running in Minikube | `false` |
| `dapr_placement.resources`                | Value of `resources` attribute. Can be used to set memory/cpu resources/limits. See the section "Resource configuration" above. Defaults to empty | `{}` |
| `dapr_placement.debug.enabled`            | Boolean value for enabling debug mode | `{}` |

### Dapr Sentry options:
| Parameter                                 | Description                                                             | Default                 |
|-------------------------------------------|-------------------------------------------------------------------------|-------------------------|
| `dapr_sentry.replicaCount`                | Number of replicas                                                      | `1`                     |
| `dapr_sentry.logLevel`                    | Log level                                                               | `info`                  |
| `dapr_sentry.image.name`                  | Docker image name (`global.registry/dapr_sentry.image.name`)            | `dapr`                  |
| `dapr_sentry.tls.issuer.certPEM`          | Issuer Certificate cert                                                 | `""`                    |
| `dapr_sentry.tls.issuer.keyPEM`           | Issuer Private Key cert                                                 | `""`                    |
| `dapr_sentry.tls.root.certPEM`            | Root Certificate cert                                                   | `""`                    |
| `dapr_sentry.trustDomain`                 | Trust domain (logical group to manage app trust relationship) for access control list | `cluster.local`  |
| `dapr_sentry.runAsNonRoot`                | Boolean value for `securityContext.runAsNonRoot`. You may have to set this to `false` when running in Minikube | `true` |
| `dapr_sentry.resources`                   | Value of `resources` attribute. Can be used to set memory/cpu resources/limits. See the section "Resource configuration" above. Defaults to empty | `{}` |
| `dapr_sentry.debug.enabled`               | Boolean value for enabling debug mode | `{}` |

### Dapr Sidecar Injector options:
| Parameter                                 | Description                                                             | Default                 |
|-------------------------------------------|-------------------------------------------------------------------------|-------------------------|
| `dapr_sidecar_injector.sidecarImagePullPolicy`      | Dapr sidecar image pull policy                                | `IfNotPresent`                     |
| `dapr_sidecar_injector.replicaCount`      | Number of replicas                                                      | `1`                     |
| `dapr_sidecar_injector.logLevel`          | Log level                                                               | `info`                  |
| `dapr_sidecar_injector.image.name`        | Docker image name for Dapr runtime sidecar to inject into an application (`global.registry/dapr_sidecar_injector.image.name`) | `daprd`|
| `dapr_sidecar_injector.injectorImage.name` | Docker image name for sidecar injector service (`global.registry/dapr_sidecar_injector.injectorImage.name`) | `dapr`|
| `dapr_sidecar_injector.webhookFailurePolicy` | Failure policy for the sidecar injector                              | `Ignore`                |
| `dapr_sidecar_injector.runAsNonRoot`      | Boolean value for `securityContext.runAsNonRoot`. You may have to set this to `false` when running in Minikube | `true` |
| `dapr_sidecar_injector.resources`         | Value of `resources` attribute. Can be used to set memory/cpu resources/limits. See the section "Resource configuration" above. Defaults to empty | `{}` |
| `dapr_sidecar_injector.debug.enabled`     | Boolean value for enabling debug mode | `{}` |
| `dapr_sidecar_injector.kubeClusterDomain` | Domain for this kubernetes cluster. If not set, will auto-detect the cluster domain through the `/etc/resolv.conf` file `search domains` content. | `cluster.local` |






## Example of highly available configuration of the control plane

This command creates three replicas of each control plane pod for an HA deployment (with the exception of the Placement pod) in the dapr-system namespace:

```
helm install dapr dapr/dapr --namespace dapr-system --set global.ha.enabled=true --wait
```

## Example of installing edge version of Dapr

This command deploys the latest `edge` version of Dapr to `dapr-system` namespace. This is useful if you want to deploy the latest version of Dapr to test a feature or some capability in your Kubernetes cluster.

```
helm install dapr dapr/dapr --namespace dapr-system --set-string global.tag=edge --wait
```

## Example of installing dapr on Minikube
Configure a values file with these options:
```yaml
dapr_dashboard:
  runAsNonRoot: false
  logLevel: DEBUG
  serviceType: NodePort  # Allows retrieving the dashboard url by running the command "minikube service list"
dapr_placement:
  runAsNonRoot: false
  logLevel: DEBUG
dapr_operator:
  runAsNonRoot: false
  logLevel: DEBUG
dapr_sentry:
  runAsNonRoot: false
  logLevel: DEBUG
dapr_sidecar_injector:
  runAsNonRoot: false
  logLevel: DEBUG
global:
  logAsJson: true
```

Install dapr:
```bash
helm install dapr dapr/dapr --namespace dapr-system --values values.yml --wait
```

## Example of debugging dapr
Rebuild dapr binaries and docker images:
```bash
make release GOOS=linux GOARCH=amd64 DEBUG=1
export DAPR_TAG=dev
export DAPR_REGISTRY=<your docker.io id>
docker login
make docker-push DEBUG=1
```
Take dapr_operator as an example, configure the corresponding `debug.enabled` option in a value file:
```yaml
global:
   registry: docker.io/<your docker.io id>
   tag: "dev-linux-amd64"
dapr_operator:
  debug:
    enabled: true
```

Step into dapr project, and install dapr:
```bash
helm install dapr charts/dapr --namespace dapr-system --values values.yml --wait
```

Find the target dapr-operator pod:
```bash
kubectl get pods -n dapr-system -o wide
```

Port forward the debugging port so that it's visible to your IDE:
```bash
kubectl port-forward dapr-operator-5c99475ffc-m9z9f 40000:40000 -n dapr-system
```
## Example of using nodeSelector option
```
helm install dapr dapr/dapr --namespace dapr-system --set global.nodeSelector.myLabel=myValue --wait
```
