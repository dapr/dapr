# Introduction

This chart deploys the Dapr control plane system services on a Kubernetes cluster using the Helm package manager.

## Chart Details

This chart installs Dapr via "child-charts":

* Dapr Component and Configuration Kubernetes CRDs
* Dapr Operator
* Dapr Sidecar injector
* Dapr Sentry
* Dapr Placement
* Dapr Scheduler

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
    helm install dapr dapr/dapr --namespace dapr-system --create-namespace --wait
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

Follow the upgrade HowTo instructions in [Upgrade Dapr with Helm](https://docs.dapr.io/operations/hosting/kubernetes/kubernetes-production/#upgrade-dapr-with-helm).


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
| Parameter                                     | Description                                                                                                                                                                                             | Default                 |
|-----------------------------------------------|---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|-------------------------|
| `global.registry`                             | Docker image registry                                                                                                                                                                                   | `ghcr.io/dapr`      |
| `global.tag`                                  | Docker image version tag                                                                                                                                                                                | latest release          |
| `global.logAsJson`                            | Json log format for control plane services                                                                                                                                                              | `false`                 |
| `global.imagePullPolicy`                      | Global Control plane service imagePullPolicy                                                                                                                                                            | `IfNotPresent`          |
| `global.imagePullSecrets`                     | Control plane service images pull secrets for docker registry. Its value can be: a string with single imagePullSecret, an array of `{name: pullSecret}` maps (Kubernetes-style), or an array of strings | `[]` |
| `global.ha.enabled`                           | Highly Availability mode enabled for control plane                                                                                                                                                      | `false`                 |
| `global.ha.replicaCount`                      | Number of replicas of control plane services in Highly Availability mode<br>Note that in HA mode, Dapr Placement has 3 replicas and that cannot be configured.                                          | `3`                   |
| `global.ha.disruption.minimumAvailable`       | Minimum amount of available instances for control plane. This can either be effective count or %.                                                                                                       | ``             |
| `global.ha.disruption.maximumUnavailable`     | Maximum amount of instances that are allowed to be unavailable for control plane. This can either be effective count or %.                                                                              | `25%`             |
| `global.prometheus.enabled`                   | Prometheus metrics enablement for control plane services                                                                                                                                                | `true`                  |
| `global.prometheus.port`                      | Prometheus scrape http endpoint port                                                                                                                                                                    | `9090`                  |
| `global.mtls.enabled`                         | Mutual TLS enablement                                                                                                                                                                                   | `true`                  |
| `global.mtls.workloadCertTTL`                 | TTL for workload cert                                                                                                                                                                                   | `24h`                   |
| `global.mtls.allowedClockSkew`                | Allowed clock skew for workload cert rotation                                                                                                                                                           | `15m`                   |
| `global.mtls.controlPlaneTrustDomain `        | Trust domain for control plane                                                                                                                                                                          | `cluster.local`         |
| `global.mtls.sentryAddress`                   | Sentry address for control plane                                                                                                                                                                        | `dapr-sentry.{{ .ReleaseNamespace  }}.svc:443` |
| `global.mtls.mountSentryToken`                | Gates whether the sentry bound service account token volume is mounted to control plane pods                                                                                                            | `true` |
| `global.extraVolumes.sentry`                  | Array of extra volumes to make available to sentry pods                                                                                                                                                 | `[]`                    |
| `global.extraVolumes.placement`               | Array of extra volumes to make available to placement pods                                                                                                                                              | `[]`                    |
| `global.extraVolumes.operator`                | Array of extra volumes to make available to operator pods                                                                                                                                               | `[]`                    |
| `global.extraVolumes.injector`                | Array of extra volumes to make available to sidecar injector pods                                                                                                                                       | `[]`                    |
| `global.extraVolumes.scheduler`               | Array of extra volumes to make available to scheduler pods                                                                                                                                       | `[]`                    |
| `global.extraVolumeMounts.sentry`             | Array of extra volume mounts to make available to sentry pod containers                                                                                                                                 | `[]`          |
| `global.extraVolumeMounts.placement`          | Array of extra volume mounts to make available to placement pod containers                                                                                                                              | `[]`          |
| `global.extraVolumeMounts.operator`           | Array of extra volume mounts to make available to operator pod containers                                                                                                                               | `[]`          |
| `global.extraVolumeMounts.injector`           | Array of extra volume mounts to make available to sidecar injector pod containers                                                                                                                       | `[]`          |
| `global.extraVolumeMounts.scheduler`          | Array of extra volume mounts to make available to scheduler pod containers                                                                                                                              | `[]`          |
| `global.dnsSuffix`                            | Kubernetes DNS suffix                                                                                                                                                                                   | `.cluster.local`        |
| `global.daprControlPlaneOs`                   | Operating System for Dapr control plane                                                                                                                                                                 | `linux`                 |
| `global.daprControlPlaneArch`                 | CPU Architecture for Dapr control plane                                                                                                                                                                 | `amd64`                 |
| `global.nodeSelector`                         | Pods will be scheduled onto a node node whose labels match the nodeSelector                                                                                                                             | `{}`         |
| `global.tolerations`                          | Pods will be allowed to schedule onto a node whose taints match the tolerations                                                                                                                         | `[]`         |
| `global.labels`                               | Custom pod labels                                                                                                                                                                                       | `{}`         |
| `global.k8sLabels`                            | Custom metadata labels                                                                                                                                                                                  | `{}`         |
| `global.issuerFilenames.ca`                   | Custom name of the file containing the root CA certificate inside the container                                                                                                                         | `ca.crt`     |
| `global.issuerFilenames.cert`                 | Custom name of the file containing the leaf certificate inside the container                                                                                                                            | `issuer.crt` |
| `global.issuerFilenames.key`                  | Custom name of the file containing the leaf certificate's key inside the container                                                                                                                      | `issuer.key` |
| `global.actors.enabled`                       | Enables the Dapr actors building block. When "false", the Dapr Placement service is not installed, and attempting to use Dapr actors will fail.                                                         | `true` |
| `global.actors.serviceName`                   | Name of the service that provides actor placement services.                                                                                                                                             | `placement` |
| `global.reminders.serviceName`                | Name of the service that provides reminders functionality. If empty (the default), uses the built-in reminders capabilities in Dapr sidecars.                                                           | |
| `global.seccompProfile`                       | SeccompProfile for Dapr control plane services                                                                                                                                                          | `""` |
| `global.rbac.namespaced`                      | Removes cluster wide permissions where applicable                                                                                                                                                       | `false` |
| `global.argoRolloutServiceReconciler.enabled` | Enable the service reconciler for Dapr-enabled Argo Rollouts                                                                                                                                            | `false`                 |
| `global.priorityClassName`                    | Adds `priorityClassName` to Dapr pods                                                                                                                                                                   | `""`                    |
| `global.scheduler.enabled`                    | Enables the Dapr Scheduler service, which enables the following building blocks: Jobs API, and for both Actors and Workflows APIs to scale. When "false", the Dapr Scheduler service is not installed, and attempting to schedule jobs in Dapr will fail. Additionally, actors and workflows will be limited in scale. | `true` |

### Dapr Operator options:
| Parameter                                  | Description                                                                                                                                                                                   | Default     |
|--------------------------------------------|-----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|-------------|
| `dapr_operator.replicaCount`               | Number of replicas                                                                                                                                                                            | `1`         |
| `dapr_operator.logLevel`                   | Log level                                                                                                                                                                                     | `info`      |
| `dapr_operator.watchInterval`              | Interval for polling pods' state (e.g. `2m`). Set to `0` to disable, or `once` to only run once when the operator starts                                                                      | `0`         |
| `dapr_operator.maxPodRestartsPerMinute`    | Maximum number of pods in an invalid state that can be restarted per minute                                                                                                                   | `20`        |
| `dapr_operator.image.name`                 | Docker image name (`global.registry/dapr_operator.image.name`)                                                                                                                                | `operator`  |
| `dapr_operator.runAsNonRoot`               | Boolean value for `securityContext.runAsNonRoot`. You may have to set this to `false` when running in Minikube                                                                                | `true`      |
| `dapr_operator.resources`                  | Value of `resources` attribute. Can be used to set memory/cpu resources/limits. See the section "Resource configuration" above. Defaults to empty                                             | `{}`        |
| `dapr_operator.debug.enabled`              | Boolean value for enabling debug mode                                                                                                                                                         | `{}`        |
| `dapr_operator.serviceReconciler.enabled`  | If false, disables the reconciler that creates Services for Dapr-enabled Deployments and StatefulSets.<br>Note: disabling this reconciler could prevent Dapr service invocation from working. | `true`      |
| `dapr_operator.watchNamespace`             | The namespace to watch for annotated Dapr resources in                                                                                                                                        | `""`        |
| `dapr_operator.deploymentAnnotations`      | Custom annotations for Dapr Operator Deployment                                                                                                                                               | `{}`        |
| `dapr_operator.apiService.annotations`     | Custom annotations for "dapr-operator" Service resource                                                                                                                                       | `{}`        |
| `dapr_operator.apiService.type`            | Type for "dapr-operator" Service resource (e.g. `ClusterIP`, `LoadBalancer`, etc)                                                                                                             | `ClusterIP` |
| `dapr_operator.webhookService.annotations` | Custom annotations for "dapr-webhook" Service resource                                                                                                                                        | `{}`        |
| `dapr_operator.webhookService.type`        | Type for "dapr-webhook" Service resource (e.g. `ClusterIP`, `LoadBalancer`, etc)                                                                                                              | `ClusterIP` |
| `dapr_operator.extraEnvVars`               | Map of (name, value) tuples to use as extra environment variables (e.g. `my-env-var: "my-val"`, etc)                                                                                          | `{}`        |

### Dapr Placement options:
| Parameter | Description | Default |
|---|---|---|
| `dapr_placement.ha`| If set to true, deploys the Placement service with 3 nodes regardless of the value of `global.ha.enabled` | `false` |
| `dapr_placement.replicationFactor` | Number of consistent hashing virtual node | `100`|
| `dapr_placement.logLevel` | Service Log level | `info`|
| `dapr_placement.image.name`                    | Service docker image name (`global.registry/dapr_placement.image.name`)                                                                                                   | `placement`                  |
| `dapr_placement.cluster.forceInMemoryLog`      | Use in-memory log store and disable volume attach when HA is true                                                                                                         | `false`                 |
| `dapr_placement.cluster.logStorePath`          | Mount path for persistent volume for log store in unix-like system when HA is true                                                                                        | `/var/run/dapr/raft-log` |
| `dapr_placement.cluster.logStoreWinPath`       | Mount path for persistent volume for log store in windows when HA is true                                                                                                 | `C:\\raft-log`          |
| `dapr_placement.volumeclaims.storageSize`      | Attached volume size | `1Gi` |
| `dapr_placement.volumeclaims.storageClassName` | Storage class name  ||
| `dapr_placement.maxActorApiLevel`                  | Sets the `max-api-level` flag which prevents the Actor API level from going above this value. The Placement service reports to all connected hosts the Actor API level as the minimum value observed in all actor hosts in the cluster. Actor hosts with a lower API level than the current API level in the cluster will not be able to connect to Placement. Setting a cap helps making sure that older versions of Dapr can connect to Placement as actor hosts, but may limit the capabilities of the actor subsystem. The default value of -1 means no cap.  | `-1` |
| `dapr_placement.minActorApiLevel`                  | Sets the `min-api-level` flag, which enforces a minimum value for the Actor API level in the cluster. | `0` |
| `dapr_placement.scaleZero` | If true, the StatefulSet is deployed with a zero scale, regardless of the values of `global.ha.enabled` or `dapr_placement.ha` | `false` |
| `dapr_placement.runAsNonRoot`                  | Boolean value for `securityContext.runAsNonRoot`. Does not apply unless `forceInMemoryLog` is set to `true`. You may have to set this to `false` when running in Minikube | `true`                 |
| `dapr_placement.fsGroup` | Integer value for `fsGroup`. Useful for adding the Placement process to the file system group that can write to the mounted database volume. | `65532`
| `dapr_placement.resources`                     | Value of `resources` attribute. Can be used to set memory/cpu resources/limits. See the section "Resource configuration" above. Defaults to empty                         | `{}`                    |
| `dapr_placement.debug.enabled`                 | Boolean value for enabling debug mode                                                                                                                                     | `{}`                    |
| `dapr_placement.metadataEnabled`               | Boolean value for enabling placement tables metadata HTTP API                                                                                                             | `false`                 |
| `dapr_placement.statefulsetAnnotations`         | Custom annotations for Dapr Placement Statefulset                                                                                                                         | `{}`    |
| `dapr_placement.service.annotations` | Custom annotations for "dapr-placement-server" Service resource | `{}` |
| `dapr_placement.extraEnvVars` | Dictionary (key: value pairs) to use as extra environment variables in the injected sidecar containers (e.g. `my-env-var: "my-val"`, etc) | `{}` |
| `dapr_placement.keepAliveTime` | Sets the interval at which the placement service sends keepalive pings to daprd on the gRPC stream to check if the connection is still alive. Lower values will lead to shorter actor rebalancing time in case of pod loss/restart, but higher network traffic during normal operation. Accepts values between `1s` and `10s`. <br> [Мore info](https://grpc.io/docs/guides/keepalive/) on gRPC keepalive | `2s` |
| `dapr_placement.keepAliveTimeout` | Sets the timeout period for daprd to respond to the placement service's keepalive pings before the placement service closes the connection. Lower values will lead to shorter actor rebalancing time in case of pod loss/restart, but higher network traffic during normal operation. Accepts values between `1s` and `10s`. <br> [Мore info](https://grpc.io/docs/guides/keepalive/) on gRPC keepalive | `3s` |
| `dapr_placement.disseminateTimeout` | Sets the timeout period for dissemination to be delayed after actor membership change (usually related to pod restarts) so as to avoid excessive dissemination during multiple pod restarts. Higher values will reduce the frequency of dissemination, but delay the table dissemination. Accepts values between `1s` and `3s` | `2s` |


### Dapr RBAC options:
| Parameter | Description | Default |
|---|---|---|
| `dapr_rbac.secretReader.enabled`          | Deploys a default secret reader Role and RoleBinding                    | `true`                  |
| `dapr_rbac.secretReader.namespace`        | Namespace for the default secret reader                                 | `default`               |

### Dapr Scheduler options:
| Parameter                                     | Description                                                                                                                                                                                                                                                                                                                                          | Default                                    |
|-----------------------------------------------|------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|--------------------------------------------|
| `dapr_scheduler.logLevel`                     | Service Log level                                                                                                                                                                                                                                                                                                                                    | `info`                                     |
| `dapr_scheduler.image.name`                   | Service docker image name (`global.registry/dapr_scheduler.image.name`)                                                                                                                                                                                                                                                                              | `scheduler`                                |
| `dapr_scheduler.cluster.etcdDataDirPath`      | Mount path for persistent volume for log store in unix-like system                                                                                                                                                                                                                                                                                   | `/var/run/data/dapr-scheduler/etcd-data-dir` |
| `dapr_scheduler.cluster.etcdDataDirWinPath`   | Mount path for persistent volume for log store in windows                                                                                                                                                                                                                                                                                            | `C:\\etcd-data-dir`                        |
| `dapr_scheduler.cluster.inMemoryStorage`      | When `dapr_scheduler.cluster.inMemoryStorage` is set to `true`, sets the Scheduler data directory volume to an ephermeral in-memory mount rather than a persistent volume claim. Note that this results in complete **data loss** of job data in Scheduler on restarts. It can only be enabled when running in standalone mode as a single instance. | `false`                                    |
| `dapr_scheduler.cluster.storageClassName`     | When set, uses this class to provision the database storage volume.                                                                                                                                                                                                                                                                                  |                                            |
| `dapr_scheduler.cluster.storageSize`          | When `dapr_scheduler.cluster.storageClassName` is set, sets the volume size request                                                                                                                                                                                                                                                                  | `1Gi`                                      |
| `dapr_scheduler.securityContext.runAsNonRoot` | Boolean value for `securityContext.runAsNonRoot`. You may have to set this to `false` when running in Minikube                                                                                                                                                                                                                                       | `true`                                     |
| `dapr_scheduler.securityContext.fsGroup`      | Integer value for `securityContext.fsGroup`. Useful for adding the Scheduler process to the file system group that can write to the mounted database volume.                                                                                                                                                                                         | `65532`                                    |
| `dapr_scheduler.resources`                    | Value of `resources` attribute. Can be used to set memory/cpu resources/limits. See the section "Resource configuration" above. Defaults to empty                                                                                                                                                                                                    | `{}`                                       |
| `dapr_scheduler.debug.enabled`                | Boolean value for enabling debug mode                                                                                                                                                                                                                                                                                                                | `{}`                                       |
| `dapr_scheduler.statefulsetAnnotations`       | Custom annotations for Dapr Scheduler Statefulset                                                                                                                                                                                                                                                                                                    | `{}`                                       |
| `dapr_scheduler.service.annotations`          | Custom annotations for "dapr-scheduler-server" Service resource                                                                                                                                                                                                                                                                                      | `{}`                                       |
| `dapr_scheduler.extraEnvVars`                 | Dictionary (key: value pairs) to use as extra environment variables in the injected sidecar containers (e.g. `my-env-var: "my-val"`, etc)                                                                                                                                                                                                            | `{}`                                       |
| `dapr_scheduler.etcdSpaceQuota`               | Space quota for etcd                                                                                                                                                                                                                                                                                                                                 | `9.2E`                                     |
| `dapr_scheduler.etcdCompactionMode`           | Compaction mode for etcd. Can be 'periodic' or 'revision'                                                                                                                                                                                                                                                                                            | `periodic`                                 |
| `dapr_scheduler.etcdSnapshotCount`            | Number of committed transactions to trigger a snapshot to disk                                                                                                                                                                                                                                                                                       | `10000`                                    |
| `dapr_scheduler.etcdMaxSnapshots`             | Maximum number of snapshot files to retain (0 is unlimited)                                                                                                                                                                                                                                                                                          | `10`                                       |
| `dapr_scheduler.etcdMaxWals`                  | Maximum number of write-ahead logs to retain (0 is unlimited)                                                                                                                                                                                                                                                                                        | `10`                                       |
| `dapr_scheduler.etcdBackendBatchLimit`        | Maximum operations before committing the backend transaction                                                                                                                                                                                                                                                                                         | `5000`                                       |
| `dapr_scheduler.etcdBackendBatchInterval`     | Maximum time before committing the backend transaction                                                                                                                                                                                                                                                                                               | `50ms`                                         |
| `dapr_scheduler.etcdDefragThresholdMB`        | Minimum number of megabytes needed to be freed for etcd to consider running defrag during bootstrap. Needs to be set to non-zero value to take effect                                                                                                                                                                                                | `100`                                      |
| `dapr_scheduler.etcdInitialElectionTickAdvance` | Whether to fast-forward initial election ticks on boot for faster election. When it is true, then local member fast-forwards election ticks to speed up “initial” leader election trigger. This benefits the case of larger election ticks. Disabling this would slow down initial bootstrap process for cross datacenter deployments. Make your own tradeoffs by configuring this flag at the cost of slow initial bootstrap.| `false`                                      |
| `dapr_scheduler.etcdMetrics`                  | Level of detail for exported metrics, specify ’extensive’ to include histogram metrics                                                                                                                                                                                                                                                               | `basic`                                    |


### Dapr Sentry options:
| Parameter | Description | Default |
|---|---|---|
| `dapr_sentry.replicaCount`          | Number of replicas                                                                                                                                      | `1`                     |
| `dapr_sentry.logLevel`              | Log level                                                                                                                                               | `info`                  |
| `dapr_sentry.image.name`            | Docker image name (`global.registry/dapr_sentry.image.name`)                                                                                            | `sentry`                  |
| `dapr_sentry.tls.issuer.certPEM`    | Issuer Certificate cert                                                                                                                                 | `""`                    |
| `dapr_sentry.tls.issuer.keyPEM`     | Issuer Private Key cert                                                                                                                                 | `""`                    |
| `dapr_sentry.tls.root.certPEM`      | Root Certificate cert                                                                                                                                   | `""`                    |
| `dapr_sentry.runAsNonRoot`          | Boolean value for `securityContext.runAsNonRoot`. You may have to set this to `false` when running in Minikube                                          | `true` |
| `dapr_sentry.resources`             | Value of `resources` attribute. Can be used to set memory/cpu resources/limits. See the section "Resource configuration" above. Defaults to empty       | `{}` |
| `dapr_sentry.debug.enabled`         | Boolean value for enabling debug mode                                                                                                                   | `{}` |
| `dapr_sentry.deploymentAnnotations` | Custom annotations for Dapr Sentry Deployment                                                                                                           | `{}`    |
| `dapr_sentry.service.annotations`   | Custom annotations for "dapr-sentry" Service resource | `{}` |
| `dapr_sentry.service.type`          | Type for "dapr-sentry" Service resource (e.g. `ClusterIP`, `LoadBalancer`, etc) | `ClusterIP` |
| `dapr_placement.extraEnvVars`       | Map of (name, value) tuples to use as extra environment variables (e.g. `my-env-var: "my-val"`, etc)                                                     | `{}`        |

### Dapr Sidecar Injector options:
| Parameter                                                 | Description                                                                                                                                                                                                                                                                                                                                                                                                                                                            | Default                                                                                                                                                                                                                   |
|-----------------------------------------------------------|------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `dapr_sidecar_injector.enabled`                           | Enable the sidecar injector                                                                                                                                                                                                                                                                                                                                                                                                                                            | `true`                                                                                                                                                                                                                    |
| `dapr_sidecar_injector.sidecarImagePullPolicy`            | Dapr sidecar image pull policy                                                                                                                                                                                                                                                                                                                                                                                                                                         | `IfNotPresent`                                                                                                                                                                                                            |
| `dapr_sidecar_injector.replicaCount`                      | Number of replicas                                                                                                                                                                                                                                                                                                                                                                                                                                                     | `1`                                                                                                                                                                                                                       |
| `dapr_sidecar_injector.logLevel`                          | Log level                                                                                                                                                                                                                                                                                                                                                                                                                                                              | `info`                                                                                                                                                                                                                    |
| `dapr_sidecar_injector.image.name`                        | Docker image name for Dapr runtime sidecar to inject into an application (`global.registry/dapr_sidecar_injector.image.name`)                                                                                                                                                                                                                                                                                                                                          | `daprd`                                                                                                                                                                                                                   |
| `dapr_sidecar_injector.injectorImage.name`                | Docker image name for sidecar injector service (`global.registry/dapr_sidecar_injector.injectorImage.name`)                                                                                                                                                                                                                                                                                                                                                            | `injector`                                                                                                                                                                                                                |
| `dapr_sidecar_injector.webhookFailurePolicy`              | Failure policy for the sidecar injector                                                                                                                                                                                                                                                                                                                                                                                                                                | `Ignore`                                                                                                                                                                                                                  |
| `dapr_sidecar_injector.runAsNonRoot`                      | Boolean value for `securityContext.runAsNonRoot` for the Sidecar Injector container itself. You may have to set this to `false` when running in Minikube                                                                                                                                                                                                                                                                                                               | `true`                                                                                                                                                                                                                    |
| `dapr_sidecar_injector.sidecarRunAsNonRoot`               | When this boolean value is true (the default), the injected sidecar containers have `runAsNonRoot: true`. You may have to set this to `false` when running Minikube                                                                                                                                                                                                                                                                                                    | `true`                                                                                                                                                                                                                    |
| `dapr_sidecar_injector.sidecarRunAsUser`                  | When set and larger than 0, sets the User ID as a `securityContext.runAsUser` value of the injected sidecar container.                                                                                                                                                                                                                                                                                                                                                 | ``                                                                                                                                                                                                                        |
| `dapr_sidecar_injector.sidecarRunAsGroup`                 | When set and larger than 0, sets the Group ID as a `securityContext.runAsGroup` value of the injected sidecar container.                                                                                                                                                                                                                                                                                                                                               | ``                                                                                                                                                                                                                        |
| `dapr_sidecar_injector.sidecarReadOnlyRootFilesystem`     | When this boolean value is true (the default), the injected sidecar containers have `readOnlyRootFilesystem: true`                                                                                                                                                                                                                                                                                                                                                     | `true`                                                                                                                                                                                                                    |
| `dapr_sidecar_injector.enableK8sDownwardAPIs`             | When set to true, uses the Kubernetes downward projection APIs to inject certain environmental variables (such as pod IP) into the daprd container.                                                                                                                                                                                                                                                                                                                    | `true`                                                                                                                                                                                                                    |
| `dapr_sidecar_injector.sidecarDropALLCapabilities`        | When this boolean valus is true, the injected sidecar containers have `securityContext.capabilities.drop: ["ALL"]`                                                                                                                                                                                                                                                                                                                                                     | `false`                                                                                                                                                                                                                   |
| `dapr_sidecar_injector.allowedServiceAccounts`            | String value for extra allowed service accounts in the format of `namespace1:serviceAccount1,namespace2:serviceAccount2`                                                                                                                                                                                                                                                                                                                                               | `""`                                                                                                                                                                                                                      |
| `dapr_sidecar_injector.allowedServiceAccountsPrefixNames` | Comma-separated list of extra allowed service accounts. Each item in the list should be in the format of namespace:serviceaccount. To match service accounts by a common prefix, you can add an asterisk (`*`) at the end of the prefix. For instance, ns1*:sa2* will match any service account that starts with sa2, whose namespace starts with ns1. For example, it will match service accounts like sa21 and sa2223 in namespaces such as ns1, ns1dapr, and so on. | `""`                                                                                                                                                                                                                         |
| `dapr_sidecar_injector.resources`                         | Value of `resources` attribute. Can be used to set memory/cpu resources/limits. See the section "Resource configuration" above. Defaults to empty                                                                                                                                                                                                                                                                                                                      | `{}`                                                                                                                                                                                                                       |
| `dapr_sidecar_injector.debug.enabled`                     | Boolean value for enabling debug mode                                                                                                                                                                                                                                                                                                                                                                                                                                  | `{}`                                                                                                                                                                                                                      |
| `dapr_sidecar_injector.kubeClusterDomain`                 | Domain for this kubernetes cluster. If not set, will auto-detect the cluster domain through the `/etc/resolv.conf` file `search domains` content.                                                                                                                                                                                                                                                                                                                      | `cluster.local`                                                                                                                                                                                                            |
| `dapr_sidecar_injector.ignoreEntrypointTolerations`       | JSON array of Kubernetes tolerations. If pod contains any of these tolerations, it will ignore the Docker image ENTRYPOINT for Dapr sidecar.                                                                                                                                                                                                                                                                                                                           | `[{\"effect\":\"NoSchedule\",\"key\":\"alibabacloud.com/eci\"},{\"effect\":\"NoSchedule\",\"key\":\"azure.com/aci\"},{\"effect\":\"NoSchedule\",\"key\":\"aws\"},{\"effect\":\"NoSchedule\",\"key\":\"huawei.com/cci\"}]` |
| `dapr_sidecar_injector.hostNetwork`                       | Enable hostNetwork mode. This is helpful when working with overlay networks such as Calico CNI and admission webhooks fail                                                                                                                                                                                                                                                                                                                                             | `false`                                                                                                                                                                                                                   |
| `dapr_sidecar_injector.healthzPort`                       | The port used for health checks. Helpful in combination with hostNetwork to avoid port collisions                                                                                                                                                                                                                                                                                                                                                                      | `8080`                                                                                                                                                                                                                    |
| `dapr_sidecar_injector.deploymentAnnotations`             | Custom annotations for Dapr Sidecar Injector Deployment                                                                                                                                                                                                                                                                                                                                                                                                                | `{}`                                                                                                                                                                                                                      |
| `dapr_sidecar_injector.service.annotations`               | Custom annotations for "dapr-sidecar-injector" Service resource                                                                                                                                                                                                                                                                                                                                                                                                        | `{}`                                                                                                                                                                                                                      |
| `dapr_sidecar_injector.service.type`                      | Type for "dapr-sidecar-injector" Service resource (e.g. `ClusterIP`, `LoadBalancer`, etc)                                                                                                                                                                                                                                                                                                                                                                              | `ClusterIP`                                                                                                                                                                                                               |
| `dapr_sidecar_injector.extraEnvVars`                      | Map of (name, value) tuples to use as extra environment variables (e.g. `my-env-var: "my-val"`, etc)                                                                                                                                                                                                                                                                                                                                                                   | `{}`                                                                                                                                                                                                                      |
| `dapr_sidecar_injector.objectSelector`                    | Custom LabelSelector to only target specific pods (e.g. `objectSelector: { matchLabels: { foo: bar } }`)                                                                                                                                                                                                                                                                                                                                                               | `{}`                                                                                                                                                                                                                       |
| `dapr_sidecar_injector.namespaceSelector`                 | Custom LabelSelector to only target pods in specific namespaces (e.g. `matchExpressions: [ { key: kubernetes.io/metadata.name, operator: NotIn, values: [ kube-system ] } ]`)                                                                                                                                                                                                                                                                                          | `{}`                                                                                                                                                                                                                       |

## Example of highly available configuration of the control plane

This command creates three replicas of each control plane pod for an HA deployment (with the exception of the Placement pod) in the dapr-system namespace:

```
helm install dapr dapr/dapr --namespace dapr-system --create-namespace --set global.ha.enabled=true --wait
```

## Example of installing edge version of Dapr

This command deploys the latest `edge` version of Dapr to `dapr-system` namespace. This is useful if you want to deploy the latest version of Dapr to test a feature or some capability in your Kubernetes cluster.

```
helm install dapr dapr/dapr --namespace dapr-system --create-namespace --set-string global.tag=edge --wait
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
helm install dapr dapr/dapr --namespace dapr-system --create-namespace --values values.yml --wait
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
```bash
helm install dapr dapr/dapr --namespace dapr-system --create-namespace --set global.nodeSelector.myLabel=myValue --wait
```
