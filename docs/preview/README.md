# Actions Preview Kit

## Overview

Actions is a programming model for writing cloud-native applications which are distributed, dynamically scaled, and loosely coupled in nature. Actions offers an eventing system on which compute units communicate with each other by exchanging messages.

The Actions runtime is designed for hyper-scale performance in the cloud and on the edge.
<br>
###### Actions Standalone Deployment
![Actions Standalone](/docs/imgs/actions_standalone.png)
<br>
<br>
###### Actions Kubernetes Deployment
![Actions on Kubernetes](/docs/imgs/actions_k8s.png)


## Included in this Preview Kit 

In this Preview Kit repository, included are the following:

1. Microsoft “Actions” Private Preview license
2. README file with instruction on how to setup Actions
3. Actions CLI binaries
4. Actions .NET SDK
5. Samples to help you get started with Actions
6. Documentation


## Release notes

This release of Actions preview kit is the very first kit version. It is focused on providing users an easy way to install and get started with Actions in both standalone and Kubernetes mode as well as "getting started" samples.

* Actions runtime 0.3.0-alpha [(release notes)](https://github.com/actionscore/actions/blob/master/docs/release_notes/v0.3.0-alpha.md)

For the full release notes, go [here](https://github.com/actionscore/actions/blob/master/docs/preview/release_notes/v0.2.0.md).


## Setup

The Actions CLI allows you to setup Actions on your local dev machine or on a Kubernetes cluster, provides debugging support, launches and manages Actions instances.

### Install as standalone

#### Prerequisites

* Download the [release](https://github.com/actionscore/cli/releases) for your OS
* Unpack it
* Move it to your desired location (for Mac/Linux - ```mv actions /usr/local/bin```. For Windows, add the executable to your System PATH.)
* **Temporary**: Run the following command to login to Docker with read-only credentials:

```
Docker login actionscore.azurecr.io --username 390401a7-d7a6-46da-b10f-3ceff7a1cdd5 --password 485b3522-59bb-4152-8938-ca8b90108af6
```

__*Note: For Windows users, run the cmd terminal in administrator mode*__

__*Note: For Linux users, if you run docker cmds with sudo, you need to use "sudo actions init*__


#### Install

```
$ actions init
⌛  Making the jump to hyperspace...
✅  Success! Get ready to rumble
```

For getting started with the Actions CLI, go [here](https://github.com/actionscore/cli/blob/master/README.md).


### Install on Kubernetes

#### Prerequisites

1. A Kubernetes cluster [(instructions)](https://kubernetes.io/docs/tutorials/kubernetes-basics/).
    
    Make sure your Kubernetes cluster is RBAC enabled.
    For AKS cluster ensure that you download the AKS cluster credentials with the following CLI

  ```cli
    az aks get-credentials -n <cluster-name> -g <resource-group>
  ```

2. *Kubectl* has been installed and configured to work with your cluster [(instructions)](https://kubernetes.io/docs/tasks/tools/install-kubectl/).

3. Download the [release](https://github.com/actionscore/cli/releases) for your OS, unpack it and move it to your desired location (for Mac/Linux - ```mv actions /usr/local/bin```. For Windows, add the executable to your System PATH.)

__*Note: For Windows users, run the cmd terminal in administrator mode*__


#### Install

```
$ actions init --kubernetes
⌛  Making the jump to hyperspace...
✅  Success! Get ready to rumble
```

### Verify installation

Once the chart installation is done, verify the Actions operator pods are running in the `actions-system` namespace:
```
$ kubectl get pods --namespace actions-system

NAME                                  READY   STATUS    RESTARTS   AGE
actions-controller-5fdc5c8d8d-dqs2f     1/1     Running   0          2d2h
actions-placement-8df4f746b-2sbs8     1/1     Running   0          2d2h
```

### (Optional) Installing Redis as Actions state store on Kubernetes using Helm

By itself, Actions installation does not include a state store. 
For getting a state store up and running on your Kubernetes cluster in a swift manner, we recommend installing Redis with Helm using the following command:
```
helm install stable/redis --rbac.create=true
```

## Contributing & Support

We are actively reviewing contributions into this repository via [issues](https://help.github.com/en/articles/creating-an-issue). 
