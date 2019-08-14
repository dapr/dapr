# Actions SDK

## Overview

Actions is a programming model for writing cloud-native applications which are distributed, dynamically scaled, and loosely coupled in nature. Actions offers an eventing system on which compute units communicate with each other by exchanging messages.

## Included in this SDK

In this SDK repository, included are the following:

1. Microsoft “Actions” Private Preview license
2. README file with instruction on how to setup Actions
3. Actions CLI binaries
4. Samples to help you get started with Actions
5. Documentation


## Release notes

This release of Actions SDK is the very first project's partners SDK. It is focused on providing users an easy way to install and get started with Actions in both standalone and Kubernetes mode as well as "getting started" samples.

* Actions runtime 0.2.0-alpha [(release notes)](https://github.com/actionscore/actions/blob/master/docs/release_notes/v0.2.0-alpha.md)
* C# SDK

For the full release notes, go [here](https://github.com/actionscore/actions/blob/master/docs/sdk/release_notes/v1.0.0.md).

## Setup

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

__*Note: For Linux users, if you run docker cmds with sudo, yuu need to use "sudo actions init*__



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

The Actions CLI allows you to setup Actions on your local dev machine or on a Kubernetes cluster, provides debugging support, launches and manages Actions instances.

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
kubectl get pods --namespace actions-system
```
 
![actions_k8s_success](/img/actions_k8s_success.png)

### (Optional) Installing Redis as Actions state store on Kubernetes using Helm

By itself, Actions installation does not include a state store. 
For getting a state store up and running on your Kubernetes cluster in a swift manner, we recommend installing Redis with Helm using the following command:
```
helm install stable/redis --rbac.create=true
```
