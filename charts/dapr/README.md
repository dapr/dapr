# Introduction

This chart deploys the Dapr control plane system services on a Kubernetes cluster using the Helm package manager.

## Chart Details

This chart installs Dapr via "child-charts":

* Dapr Component and Configuration Kubernetes CRDs
* Dapr Operator
* Dapr Sidecar injector
* Dapr Sentry
* Dapr Placement

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
| `global.tag`                              | Docker image version tag                                                | latest release          |
| `global.logAsJson`                        | Json log format for control plane services                              | `false`                 |
| `global.imagePullPolicy`                  | Global Control plane service imagePullPolicy                            | `IfNotPresent`          |
| `global.imagePullSecrets`                 | Control plane service images pull secrets for docker registry           | `""`                    |
| `global.ha.enabled`                       | Highly Availability mode enabled for control plane                      | `false`                 |
| `global.ha.replicaCount`                  | Number of replicas of control plane services in Highly Availability mode  | `3`                   |
| `global.ha.disruption.minimumAvailable`   | Minimum amount of available instances for control plane. This can either be effective count or %. | ``             |
| `global.ha.disruption.maximumUnavailable` | Maximum amount of instances that are allowed to be unavailable for control plane. This can either be effective count or %. | `25%`             |
| `global.prometheus.enabled`               | Prometheus metrics enablement for control plane services                | `true`                  |
| `global.prometheus.port`                  | Prometheus scrape http endpoint port                                    | `9090`                  |
| `global.mtls.enabled`                     | Mutual TLS enablement                                                   | `true`                  |
| `global.mtls.workloadCertTTL`             | TTL for workload cert                                                   | `24h`                   |
| `global.mtls.allowedClockSkew`            | Allowed clock skew for workload cert rotation                           | `15m`                   |
| `global.dnsSuffix`                        | Kuberentes DNS suffix                                                   | `.cluster.local`        |
| `global.daprControlPlaneOs`               | Operating System for Dapr control plane                                 | `linux`                 |
| `global.daprControlPlaneArch`             | CPU Architecture for Dapr control plane                                 | `amd64`                 |
| `global.nodeSelector`                     | Pods will be scheduled onto a node node whose labels match the nodeSelector        | `{}`         |
| `global.tolerations`                      | Pods will be allowed to schedule onto a node whose taints match the tolerations    | `{}`         |
| `global.labels`                           | Custom pod labels                                                                  | `{}`         |
| `global.k8sLabels`                        | Custom metadata labels                                                             | `{}`         |
| `global.issuerFilenames.ca`               | Custom name of the file containing the root CA certificate inside the container    | `ca.crt`     |
| `global.issuerFilenames.cert`             | Custom name of the file containing the leaf certificate inside the container       | `issuer.crt` |
| `global.issuerFilenames.key`              | Custom name of the file containing the leaf certificate's key inside the container | `issuer.key` |
| `global.actors.enabled`                   | Enables the Dapr actors building block. When "false", the Dapr Placement serice is not installed, and attempting to use Dapr actors will fail. | `true`                  |
| `global.rbac.namespaced`                  | Removes cluster wide permissions where applicable  | `false` |
| `global.argoRolloutServiceReconciler.enabled` | Enable the service reconciler for Dapr-enabled Argo Rollouts         | `false` |

### Dapr Operator options:
| Parameter                                 | Description                                                             | Default                 |
|-------------------------------------------|-------------------------------------------------------------------------|-------------------------|
| `dapr_operator.replicaCount`              | Number of replicas                                                      | `1`                     |
| `dapr_operator.logLevel`                  | Log level                                                               | `info`                  |
| `dapr_operator.watchInterval`             | Interval for polling pods' state (e.g. `2m`). Set to `0` to disable, or `once` to only run once when the operator starts | `0` |
| `dapr_operator.maxPodRestartsPerMinute`   | Maximum number of pods in an invalid state that can be restarted per minute | `20`                |
| `dapr_operator.image.name`                | Docker image name (`global.registry/dapr_operator.image.name`)          | `dapr`                  |
| `dapr_operator.runAsNonRoot`              | Boolean value for `securityContext.runAsNonRoot`. You may have to set this to `false` when running in Minikube | `true` |
| `dapr_operator.resources`                 | Value of `resources` attribute. Can be used to set memory/cpu resources/limits. See the section "Resource configuration" above. Defaults to empty | `{}` |
| `dapr_operator.debug.enabled`             | Boolean value for enabling debug mode | `{}` |
| `dapr_operator.serviceReconciler.enabled`| If false, disables the reconciler that creates Services for Dapr-enabled Deployments and StatefulSets.<br>Note: disabling this reconciler could prevent Dapr service invocation from working. | `true` |
| `dapr_operator.watchNamespace`            | The namespace to watch for annotated Dapr resources in | `""` |

### Dapr Placement options:
| Parameter                                 | Description                                                             | Default                 |
|-------------------------------------------|-------------------------------------------------------------------------|-------------------------|
| `dapr_placement.replicationFactor`        | Number of consistent hashing virtual node | `100`   |
| `dapr_placement.logLevel`                 | Service Log level                                                       | `info`                  |
| `dapr_placement.image.name`               | Service docker image name (`global.registry/dapr_placement.image.name`) | `dapr`   |
| `dapr_placement.cluster.forceInMemoryLog` | Use in-memory log store and disable volume attach when `global.ha.enabled` is true | `false`   |
| `dapr_placement.cluster.logStorePath`     | Mount path for persistent volume for log store in unix-like system when `global.ha.enabled` is true | `/var/run/dapr/raft-log`   |
| `dapr_placement.cluster.logStoreWinPath`  | Mount path for persistent volume for log store in windows when `global.ha.enabled` is true | `C:\\raft-log`   |
| `dapr_placement.volumeclaims.storageSize` | Attached volume size | `1Gi`   |
| `dapr_placement.volumeclaims.storageClassName` | storage class name |    |
| `dapr_placement.runAsNonRoot`             | Boolean value for `securityContext.runAsNonRoot`. Does not apply unless `forceInMemoryLog` is set to `true`. You may have to set this to `false` when running in Minikube | `false` |
| `dapr_placement.resources`                | Value of `resources` attribute. Can be used to set memory/cpu resources/limits. See the section "Resource configuration" above. Defaults to empty | `{}` |
| `dapr_placement.debug.enabled`            | Boolean value for enabling debug mode | `{}` |

### Dapr RBAC options:
| Parameter                                 | Description                                                             | Default                 |
|-------------------------------------------|-------------------------------------------------------------------------|-------------------------|
| `dapr_rbac.secretReader.enabled`          | Deploys a default secret reader Role and RoleBinding                    | `true`                  |
| `dapr_rbac.secretReader.namespace`        | Namespace for the default secret reader                                 | `default`               |

### Dapr Sentry options:
| Parameter                                 | Description                                                             | Default                 |
|-------------------------------------------|-------------------------------------------------------------------------|-------------------------|
| `dapr_sentry.replicaCount`                | Number of replicas                                                      | `1`                     |
| `dapr_sentry.logLevel`                    | Log level                                                               | `info`                  |
| `dapr_sentry.image.name`                  | Docker image name (`global.registry/dapr_sentry.image.name`)            | `dapr`                  |
| `dapr_sentry.tls.issuer.certPEM`          | Issuer Certificate cert                                                 | `""`                    |
| `dapr_sentry.tls.issuer.keyPEM`           | Issuer Private Key cert                                                 | `""`                    |
| `dapr_sentry.tls.root.certPEM`            | Root Certificate cert                                                   | `""`                    |
| `dapr_sentry.tokenAudience`               | Expected audience for tokens; multiple values can be separated by a comma. Defaults to the audience expected by the Kubernetes control plane if not set | `""` |
| `dapr_sentry.trustDomain`                 | Trust domain (logical group to manage app trust relationship) for access control list | `cluster.local`  |
| `dapr_sentry.runAsNonRoot`                | Boolean value for `securityContext.runAsNonRoot`. You may have to set this to `false` when running in Minikube | `true` |
| `dapr_sentry.resources`                   | Value of `resources` attribute. Can be used to set memory/cpu resources/limits. See the section "Resource configuration" above. Defaults to empty | `{}` |
| `dapr_sentry.debug.enabled`               | Boolean value for enabling debug mode | `{}` |

### Dapr Sidecar Injector options:
| Parameter                                 | Description                                                                                                                                                                                                                                                                                                                                                                                                                                                            | Default                 |
|-------------------------------------------|------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|-------------------------|
| `dapr_sidecar_injector.enabled`           | Enable the sidecar injector                                                                                                                                                                                                                                                                                                                                                                                                                                            | `true`                |
| `dapr_sidecar_injector.sidecarImagePullPolicy`      | Dapr sidecar image pull policy                                                                                                                                                                                                                                                                                                                                                                                                                                         | `IfNotPresent`                     |
| `dapr_sidecar_injector.replicaCount`      | Number of replicas                                                                                                                                                                                                                                                                                                                                                                                                                                                     | `1`                     |
| `dapr_sidecar_injector.logLevel`          | Log level                                                                                                                                                                                                                                                                                                                                                                                                                                                              | `info`                  |
| `dapr_sidecar_injector.image.name`        | Docker image name for Dapr runtime sidecar to inject into an application (`global.registry/dapr_sidecar_injector.image.name`)                                                                                                                                                                                                                                                                                                                                          | `daprd`|
| `dapr_sidecar_injector.injectorImage.name` | Docker image name for sidecar injector service (`global.registry/dapr_sidecar_injector.injectorImage.name`)                                                                                                                                                                                                                                                                                                                                                            | `dapr`|
| `dapr_sidecar_injector.webhookFailurePolicy` | Failure policy for the sidecar injector                                                                                                                                                                                                                                                                                                                                                                                                                                | `Ignore`                |
| `dapr_sidecar_injector.runAsNonRoot`      | Boolean value for `securityContext.runAsNonRoot` for the Sidecar Injector container itself. You may have to set this to `false` when running in Minikube                                                                                                                                                                                                                                                                                                               | `true` |
| `dapr_sidecar_injector.sidecarRunAsNonRoot` | When this boolean value is true (the default), the injected sidecar containers have `runAsRoot: true`. You may have to set this to `false` when running Minikube                                                                                                                                                                                                                                                                                                       | `true` |
| `dapr_sidecar_injector.sidecarReadOnlyRootFilesystem` | When this boolean value is true (the default), the injected sidecar containers have `readOnlyRootFilesystem: true`                                                                                                                                                                                                                                                                                                                                                     | `true` |
| `dapr_sidecar_injector.sidecarDropALLCapabilities` | When this boolean valus is true, the injected sidecar containers have `securityContext.capabilities.drop: ["ALL"]` | `false` |
| `dapr_sidecar_injector.allowedServiceAccounts` | String value for extra allowed service accounts in the format of `namespace1:serviceAccount1,namespace2:serviceAccount2`                                                                                                                                                                                                                                                                                                                                               | `""` |
| `dapr_sidecar_injector.allowedServiceAccountsPrefixNames` | Comma-separated list of extra allowed service accounts. Each item in the list should be in the format of namespace:serviceaccount. To match service accounts by a common prefix, you can add an asterisk (`*`) at the end of the prefix. For instance, ns1*:sa2* will match any service account that starts with sa2, whose namespace starts with ns1. For example, it will match service accounts like sa21 and sa2223 in namespaces such as ns1, ns1dapr, and so on. | `""` |
| `dapr_sidecar_injector.resources`         | Value of `resources` attribute. Can be used to set memory/cpu resources/limits. See the section "Resource configuration" above. Defaults to empty                                                                                                                                                                                                                                                                                                                      | `{}` |
| `dapr_sidecar_injector.debug.enabled`     | Boolean value for enabling debug mode                                                                                                                                                                                                                                                                                                                                                                                                                                  | `{}` |
| `dapr_sidecar_injector.kubeClusterDomain` | Domain for this kubernetes cluster. If not set, will auto-detect the cluster domain through the `/etc/resolv.conf` file `search domains` content.                                                                                                                                                                                                                                                                                                                      | `cluster.local` |
| `dapr_sidecar_injector.ignoreEntrypointTolerations` | JSON array of Kubernetes tolerations. If pod contains any of these tolerations, it will ignore the Docker image ENTRYPOINT for Dapr sidecar.                                                                                                                                                                                                                                                                                                                           | `[{\"effect\":\"NoSchedule\",\"key\":\"alibabacloud.com/eci\"},{\"effect\":\"NoSchedule\",\"key\":\"azure.com/aci\"},{\"effect\":\"NoSchedule\",\"key\":\"aws\"},{\"effect\":\"NoSchedule\",\"key\":\"huawei.com/cci\"}]` |
| `dapr_sidecar_injector.hostNetwork` | Enable hostNetwork mode. This is helpful when working with overlay networks such as Calico CNI and admission webhooks fail                                                                                                                                                                                                                                                                                                                                             | `false` |
| `dapr_sidecar_injector.healthzPort` | The port used for health checks. Helpful in combination with hostNetwork to avoid port collisions                                                                                                                                                                                                                                                                                                                                                                      | `8080` |

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
