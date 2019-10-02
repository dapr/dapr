# Dapr - A high performance, lightweight serverless runtime for cloud and edge

[![Build Status](https://dev.azure.com/azure-octo/Dapr/_apis/build/status/builds/dapr%20build?branchName=master)](https://dev.azure.com/azure-octo/Dapr/_build/latest?definitionId=5&branchName=master)

__Note: This repo is currently under heavy development.
As long as this note is here, consider docs not up-to-date at all times. Edge builds with potential bugs and/or breaking changes will be pushed daily.__

Dapr is a programming model for writing cloud-native applications which are distributed, dynamically scaled, and loosely coupled in nature. Dapr offers an eventing system on which compute units communicate with each other by exchanging messages.
<br>
<br>
Dapr injects a side-car container/process to each compute unit. The side-car interacts with event triggers and communicates with the compute unit via standard HTTP or GRPC protocols. This enables Dapr to support all existing and future programming languages without requiring developers to import frameworks or libraries.
<br>
Dapr offers built-in state management, reliable messaging (at least once delivery), triggers and bindings through standard HTTP verbs or GRPC interfaces. This allows developers to write stateless, stateful and actor-like services following the same programming paradigm. And developers can freely choose consistency model, threading model and message delivery patterns.

Dapr runs natively on Kubernetes, as a standalone binary on a developer's machine, on an IoT device, or as a container than can be injected into any system, in the cloud or on-premises.

Dapr uses pluggable state stores and message buses such as Redis as well as gRPC to offer a wide range of communication methods, including direct dapr-to-dapr using gRPC and async Pub-Sub with guaranteed delivery and at-least-once semantics.

The Dapr runtime is designed for hyper-scale performance in the cloud and on the edge.
<br>
###### Dapr Standalone Deployment
![Dapr Standalone](/docs/imgs/dapr_standalone.png)
<br>
<br>
###### Dapr Kubernetes Deployment
![Dapr on Kubernetes](/docs/imgs/dapr_k8s.png)

## Why Dapr

Writing a successful distributed application is hard. Dapr brings proven patterns and practices to ordinary developers. It unifies Functions and Actors into a simple, consistent programming model. It supports all programming languages without framework lock-in. Developers are not exposed to low-level primitives such as threading, concurrency control, partition and scaling. Instead, they can write their code just like implementing a simple web server using familiar web frameworks of their choice.

Dapr is flexible in threading model and state consistency model. Developers can leverage multi-threading if they choose to, and they can choose among different consistency models. This flexibility enables power developers to implement advanced scenarios without artificial constraints.  On the other hand, a developer can choose to stay with single-threaded calls as she’s enjoyed in other Actor frameworks. And Dapr is unique because developers can transit between these models without rewriting their code. 


## Features

* Supports all programming languages
* Does not require any libraries or SDKs
* Built-in Eventing system
* Built-in service discovery
* Asynchronous Pub-Sub with guaranteed delivery and at-least-once semantics
* Dapr to Dapr request response using gRPC
* State management - persist and restore
* Choice of concurrency model: Single-Threaded or Multiple
* Triggers (Azure, AWS, GCP, etc.)
* Lightweight (20.4MB binary, 4MB physical memory needed)
* Runs natively on Kubernetes
* Easy to debug - runs locally on your machine

## Setup

### Install as standalone

#### Prerequisites

Download the Dapr CLI [release](https://github.com/dapr/cli/releases) for your OS, unpack it and move it to your desired location (for Mac/Linux - ```mv dapr /usr/local/bin```. For Windows, add the executable to your System PATH. (e.g. create c:\dapr)


__*Note: For Windows users, run the cmd terminal in administrator mode*__

__*Note: For Linux users, if you run docker cmds with sudo, you need to use "sudo dapr init*__

#### Install

```
$ dapr init
⌛  Making the jump to hyperspace...
Downloading binaries and setting up components
✅  Success! Dapr is up and running
```

To see that Dapr has been installed successful, from a command prompt run `docker ps` command and see that `actionscore.azurecr.io/dapr:latest` and `redis` container images are both running.


For getting started with the Dapr CLI, go [here](https://github.com/dapr/cli).

### Install on Kubernetes

#### Prerequisites

1. A Kubernetes cluster [(instructions)](https://kubernetes.io/docs/tutorials/kubernetes-basics/).
    
    Make sure your Kubernetes cluster is RBAC enabled.
    For AKS cluster ensure that you download the AKS cluster credentials with the following CLI

  ```cli
    az aks get-credentials -n <cluster-name> -g <resource-group>
  ```

2. *Kubectl* has been installed and configured to work with your cluster [(instructions)](https://kubernetes.io/docs/tasks/tools/install-kubectl/).

The Dapr CLI allows you to setup Dapr on your local dev machine or on a Kubernetes cluster, provides debugging support, launches and manages Dapr instances.

3. If you are planning to install Dapr on Kubernetes using it's CLI, download the [release](https://github.com/dapr/cli/releases) for your OS, unpack it and move it to your desired location (for Mac/Linux - ```mv dapr /usr/local/bin```. For Windows, add the executable to your System PATH.)

__*Note: For Windows users, run the cmd terminal in administrator mode*__

### Install on Kubernetes using Dapr CLI

To setup Dapr on Kubernetes:

```
$ dapr init --kubernetes
⌛  Making the jump to hyperspace...
✅  Success! Get ready to rumble
```

### Install on Kubernetes using Helm (Advanced)

**Note**: This "how-to" is a light version of our more detailed README on how to install Dapr on Kubernetes using Helm. For the detailed version, go [here](https://github.com/dapr/dapr/blob/master/charts/dapr-operator/README.md).

Dapr' Helm chart is hosted in an [Azure Container Registry](https://azure.microsoft.com/en-us/services/container-registry/),
which does not yet support anonymous access to charts therein. Until this is
resolved, adding the Helm repository from which Dapr can be installed requires
use of a shared set of read-only credentials.

Make sure helm is initialized in your running kubernetes cluster.

For more details on initializing helm, go [here](https://docs.helm.sh/helm/#helm)

1. Add Azure Container Registry as an helm repo
    ```
    helm repo add dapr https://actionscore.azurecr.io/helm/v1/repo \
    --username 390401a7-d7a6-46da-b10f-3ceff7a1cdd5 \
    --password 485b3522-59bb-4152-8938-ca8b90108af6
    ```

2. Install the Dapr chart on your cluster in the dapr-system namespace:
    ```
    helm install dapr/dapr-operator --name dapr --namespace dapr-system
    ``` 

### Verify installation

Once the chart installation is done, verify the Dapr operator pods are running in the `dapr-system` namespace:
```
kubectl get pods --namespace dapr-system
```
 
![dapr_helm_success](/charts/dapr-operator/img/dapr_helm_success.png)

### (Optional) Installing Redis as Dapr state store on Kubernetes using Helm

By itself, Dapr installation does not include a state store. 
For getting a state store up and running on your Kubernetes cluster in a swift manner, we recommend installing Redis with Helm using the following command:
```
helm install stable/redis --set rbac.create=true
```   

## Samples

For more samples, please look at [samples](https://github.com/dapr/samples).
* [Run Dapr Locally](https://github.com/dapr/samples/tree/master/1.hello-world)
* [Run Dapr in Kubernetes](https://github.com/dapr/samples/tree/master/2.hello-kubernetes)

## Contributing to the project

* [Development Guide](./docs/development/development.md)