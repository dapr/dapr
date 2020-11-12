  
# Dapr $dapr_version

We're happy to announce the release of Dapr $dapr_version!

We would like to extend our thanks to all the new and existing contributors who helped make this release happen.

**Highlights**

If you're new to Dapr, visit the [getting started](https://docs.dapr.io/getting-started/) page and familiarize yourself with Dapr.

Docs have been updated with all the new features and changes of this release. To get started with new capabilities introduced in this release, go to the [Concepts](https://docs.dapr.io/concepts/) and the [Developing applications](https://docs.dapr.io/developing-applications/).

$warnings

See [this](#upgrading-to-dapr-$dapr_version) section on upgrading Dapr to version $dapr_version.

## New in this release

$dapr_changes

## Upgrading to Dapr $dapr_version

To upgrade to this release of Dapr, follow the steps here to ensure a smooth upgrade. You know, the one where you don't get red errors on the terminal.. we all hate that, right?

### Local Machine / Self-hosted

Uninstall Dapr using the CLI you currently have installed. Note that this will remove the default $HOME/.dapr directory, binaries and all containers dapr_redis, dapr_placement and dapr_zipkin. Linux users need to run sudo if docker command needs sudo:

```bash
dapr uninstall --all
```

Next, follow [these](https://github.com/dapr/cli#installing-dapr-cli) instructions to install the latest CLI version, or alternatively download the latest and greatest release from [here](https://github.com/dapr/cli/releases) and put the `dapr` binary in your PATH.

Once you have installed the CLI, run:

```bash
dapr init
```

Wait for the update to finish,  ensure you are using the latest version of Dapr($dapr_version) with:

```bash
$ dapr --version

CLI version: $dapr_version
Runtime version: $dapr_version
```

### Kubernetes

#### Upgrading from previous version

If you previously installed Dapr using Helm, you can upgrade Dapr to a new version.
If you installed Dapr using the CLI, go [here](#starting-fresh-install-on-a-cluster).

##### 1. Get the latest CLI

Get the latest version of the Dapr CLI as outlined above, and put it in your path.
You can also use the helper scripts outlined [here](https://github.com/dapr/cli#installing-dapr-cli) to get the latest version.

##### 2. Upgrade existing cluster

First, add new Dapr helm repository(see [breaking changes](#breaking-changes)) and update your Helm repos:

```bash
helm repo add dapr https://dapr.github.io/helm-charts/ --force-update
helm repo update
```

Run the following commands to upgrade the Dapr control plane system services and data plane services:

* Export certificates

  ```
  dapr mtls export -o ./certs
  ```

* Updating Dapr control plane pods
  * Using the certs exported above, run the following command:
    ```
    helm upgrade dapr dapr/dapr --version $dapr_version --namespace dapr-system --reset-values --set-file dapr_sentry.tls.root.certPEM=./certs/ca.crt --set-file dapr_sentry.tls.issuer.certPEM=./certs/issuer.crt --set-file dapr_sentry.tls.issuer.keyPEM=./certs/issuer.key
    ```

  * Wait until all the pods are in Running state:

    ```
    kubectl get pods -w -n dapr-system
    ```

  * Verify the control plane is updated and healthy:

    ```
    $ dapr status -k

    NAME                   NAMESPACE    HEALTHY  STATUS   REPLICAS  VERSION  AGE  CREATED
    dapr-dashboard         dapr-system  True     Running  1         $dapr_dashboard_version    15s  $today 13:07.39
    dapr-sidecar-injector  dapr-system  True     Running  1         $dapr_version   15s  $today 13:07.39
    dapr-sentry            dapr-system  True     Running  1         $dapr_version   15s  $today 13:07.39
    dapr-operator          dapr-system  True     Running  1         $dapr_version   15s  $today 13:07.39
    dapr-placement         dapr-system  True     Running  1         $dapr_version   15s  $today 13:07.39
    ```

* Updating the data plane (sidecars)
  * Next, issue a rolling update to your Dapr enabled deployments. When the pods restart, the new Dapr sidecar version will be picked up.

    ```
    kubectl rollout restart deploy/<DEPLOYMENT-NAME>
    ```

All done!

#### Starting fresh install on a cluster 

If you previously installed Dapr on your Kubernetes cluster using the Dapr CLI, run:

*Note: Make sure you're uninstalling with your existing CLI version*

```bash
dapr uninstall --kubernetes
```

It's fine to ignore any errors that might show up.

If you previously installed Dapr using __Helm 2.X__:

```bash
helm del --purge dapr
```

If you previously installed Dapr using __Helm 3.X__:

```bash
helm uninstall dapr -n dapr-system
```

Update the Dapr repo:

```bash
helm repo update
```

If you installed Dapr with Helm to a namespace other than `dapr-system`, modify the uninstall command above to account for that.

You can now follow [these](https://docs.dapr.io/getting-started/install-dapr/#install-with-helm-advanced) instructions on how to install Dapr using __Helm 3__.

Alternatively, you can use the newer version of CLI:

```
dapr init --kubernetes
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


## Acknowledgements

Thanks to everyone who made this release possible!

$dapr_contributors