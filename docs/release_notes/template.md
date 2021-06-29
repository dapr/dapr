
# Dapr $dapr_version

We're happy to announce the release of Dapr $dapr_version!

We would like to extend our thanks to all the new and existing contributors who helped make this release happen.

**Highlights**

If you're new to Dapr, visit the [getting started](https://docs.dapr.io/getting-started/) page and familiarize yourself with Dapr.

Docs have been updated with all the new features and changes of this release. To get started with new capabilities introduced in this release, go to the [Concepts](https://docs.dapr.io/concepts/) and the [Developing applications](https://docs.dapr.io/developing-applications/).

$warnings

See [this](#upgrading-to-dapr-$dapr_version) section on upgrading Dapr to version $dapr_version.

## Acknowledgements

Thanks to everyone who made this release possible!

$dapr_contributors

## New in this release

$dapr_changes

## Upgrading to Dapr $dapr_version

To upgrade to this release of Dapr, follow the steps here to ensure a smooth upgrade. You know, the one where you don't get red errors on the terminal.. we all hate that, right?

### Local Machine / Self-hosted

Uninstall Dapr using the CLI you currently have installed. Note that this will remove the default $HOME/.dapr directory, binaries and all containers dapr_redis, dapr_placement and dapr_zipkin. Linux users need to run sudo if docker command needs sudo:

```bash
dapr uninstall --all
```

For RC releases like this, download the latest and greatest release from [here](https://github.com/dapr/cli/releases) and put the `dapr` binary in your PATH.

Once you have installed the CLI, run:

```bash
dapr init --runtime-version=$dapr_version
```

Wait for the update to finish,  ensure you are using the latest version of Dapr($dapr_version) with:

```bash
$ dapr --version

CLI version: $dapr_version
Runtime version: $dapr_version
```

### Kubernetes

#### Upgrading from previous version

You can perform zero-downtime upgrades using both Helm 3 and the Dapr CLI.

##### Upgrade using the CLI

Download the latest RC release from [here](https://github.com/dapr/cli/releases) and put the `dapr` binary in your PATH.

To upgrade Dapr, run:

```
dapr upgrade --runtime-version $dapr_version -k
```

To upgrade with high availability mode:

```
dapr upgrade --runtime-version $dapr_version --enable-ha=true -k
```

Wait until the operation is finished and check your status with `dapr status -k`.

All done!

*Note: Make sure your deployments are restarted to pick the latest version of the Dapr sidecar*

##### Upgrade using Helm

To upgrade Dapr using Helm, run:

```
helm repo add dapr https://dapr.github.io/helm-charts/
helm repo update

helm upgrade dapr dapr/dapr --version $dapr_version --namespace=dapr-system --wait
```

Wait until the operation is finished and check your status with `dapr status -k`.

All done!

*Note: Make sure your deployments are restarted to pick the latest version of the Dapr sidecar*

#### Starting a fresh install on a cluster

Please see [how to deploy Dapr on a Kubernetes cluster](https://docs.dapr.io/operations/hosting/kubernetes/kubernetes-deploy/) for a complete guide to installing Dapr on Kubernetes

You can use Helm 3 to install Dapr:
```
helm repo add dapr https://dapr.github.io/helm-charts/
helm repo update

kubectl create namespace dapr-system

helm install dapr dapr/dapr --version $dapr_version --namespace dapr-system --wait
```

Alternatively, you can use the latest version of CLI:

```
dapr init --runtime-version=$dapr_version -k
```

##### Post installation

Verify the control plane pods are running and are healthy:

```
$ dapr status -k
  NAME                   NAMESPACE    HEALTHY  STATUS   REPLICAS  VERSION  AGE  CREATED
  dapr-dashboard         dapr-system  True     Running  1         $dapr_dashboard_version    15s  $today 13:07.39
  dapr-sidecar-injector  dapr-system  True     Running  1         $dapr_version   15s  $today 13:07.39
  dapr-sentry            dapr-system  True     Running  1         $dapr_version   15s  $today 13:07.39
  dapr-operator          dapr-system  True     Running  1         $dapr_version   15s  $today 13:07.39
  dapr-placement         dapr-system  True     Running  1         $dapr_version   15s  $today 13:07.39
```

After Dapr $dapr_version has been installed, perform a rolling restart for your deployments to pick up the new version of the sidecar.
This can be done with:

```
kubectl rollout restart deploy/<deployment-name>
```

## Breaking Changes

$dapr_breaking_changes
