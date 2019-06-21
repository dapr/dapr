# Actions - A highly performant, lightweight serverless runtime for cloud and edge

__Note: This repo is currently under heavy development.
As long as this note is here, consider docs not up-to-date at all times. Edge builds with potential bugs and/or breaking changes will be pushed daily.__

Actions is a programming model for writing cloud-native applications which are distributed, dynamically scaled, and loosely coupled in nature. Actions offers an eventing system on which compute units communicate with each other by exchanging messages.
<br>
<br>
Actions injects a side-car container/process to each compute unit. The side-car interacts with event triggers and communicates with the compute unit via standard HTTP or GRPC protocols. This enables Actions to support all existing and future programming languages without requiring developers to import frameworks or libraries.
<br>
Actions offers built-in state management, reliable messaging (at least once delivery), triggers and bindings through standard HTTP verbs or GRPC interfaces. This allows developers to write stateless, stateful and actor-like services following the same programming paradigm. And developers can freely choose consistency model, threading model and message delivery patterns.

Actions runs natively on Kubernetes, as a standalone binary on a developer's machine, on an IoT device, or as a container than can be injected into any system, in the cloud or on-premises.

Actions uses pluggable state stores and message buses such as Redis as well as gRPC to offer a wide range of communication methods, including direct action-to-action using gRPC and async Pub-Sub with guaranteed delivery and at-least-once semantics.

The Actions runtime is designed for hyper-scale performance in the cloud and on the edge.
<br>

![Actions Logical Design](/docs/imgs/actions_logical_design.png)

## Why Actions

Writing a successful distributed application is hard. Actions brings proven patterns and practices to ordinary developers. It unifies Functions and Actors into a simple, consistent programming model. It supports all programming languages without framework lock-in. Developers are not exposed to low-level primitives such as threading, concurrency control, partition and scaling. Instead, they can write their code just like implementing a simple web server using familiar web frameworks of their choice.

Actions is flexible in threading model and state consistency model. Developers can leverage multi-threading if they choose to, and they can choose among different consistency models. This flexibility enables power developers to implement advanced scenarios without artificial constraints.  On the other hand, a developer can choose to stay with single-threaded calls as she’s enjoyed in other Actor frameworks. And Actions is unique because developers can transit between these models without rewriting their code. 


## Features

* Supports all programming languages
* Does not require any libraries or SDKs
* Built-in Eventing system
* Built-in service discovery
* Asynchronous Pub-Sub with guaranteed delivery and at-least-once semantics
* Action to Action request response using gRCP
* State management - persist and restore
* Choice of concurrency model: Single-Threaded or Multiple
* Triggers (Azure, AWS, GCP, etc.)
* Lightweight (20.4MB binary, 4MB physical memory needed)
* Runs natively on Kubernetes
* Easy to debug - runs locally on your machine

## Setup

### Install on Kubernetes

#### Prerequisites

1. A Kubernetes cluster [(instructions)](https://kubernetes.io/docs/tutorials/kubernetes-basics/).
    
    Make sure your Kubernetes cluster is RBAC enabled.
    For AKS cluster ensure that you download the AKS cluster credentials with the following CLI

  ```cli
    az aks get-credentials -n <cluster-name> -g <resource-group>
  ```

2. *Kubectl* has been installed and configured to work with your cluster [(instructions)](https://kubernetes.io/docs/tasks/tools/install-kubectl/).


#### Clone the repo

```
git clone https://github.com/actionscore/actions.git
cd actions
```

#### Deploy Actions

```
kubectl apply -f ./deploy
```

Watch for the Actions control plane pod to be in ```Running``` state:

```
kubectl get pods --selector=app=actions
```

Yay, Actions have been deployed on your cluster!


### Install as standalone

#### Prerequisites

1. The Go language environment [(instructions)]( https://golang.org/doc/install).

    Make sure you've already configured your GOPATH and GOROOT environment variables.

#### Clone the repo

```
cd $GOPATH/src
mkdir -p github.com/actionscore/actions
git clone https://github.com/actionscore/actions.git github.com/actionscore/actions
```

#### Build the action binary

```
cd $GOPATH/src/github.com/actionscore/actions/cmd/action
go build -o action
```

## Usage

Check out the following tutorials:

* [From Zero to Hero with Kubernetes](docs/getting_started/zero_to_hero/README.md)
* [Enable state management with Redis](docs/concepts/state/redis.md)
* [Enable state management with CosmosDB](docs/concepts/state/cosmosdb.md)<br><br>
* [Setup an AWS SQS Event Source](docs/aws_sqs.md)
* [Setup an AWS SNS Event Source](docs/aws_sns.md)
* [Setup an Azure Event Hubs Event Source](docs/azure_eventhubs.md)
* [Setup a Google Cloud Storage Event Source](docs/gcp_storage.md)
* [Setup an HTTP Event Source](docs/http.md)<br><br>
* [Getting started with C#](docs/getting_started/c%23/tutorial.md)
* [Getting started with Go](docs/getting_started/go/tutorial.md)
* [Getting started with Node.JS](docs/getting_started/node/tutorial.md)
* [Getting started with Python](docs/getting_started/python/tutorial.md)
* [Getting started with Java](docs/getting_started/java/tutorial.md)
* [Getting started with Rust](docs/getting_started/rust/tutorial.md)
* [Getting started with Ruby](docs/getting_started/ruby/tutorial.md)
* [Getting started with C++](docs/getting_started/c++/tutorial.md)
* [Getting started with PHP](docs/getting_started/php/tutorial.md)
* [Getting started with Scala](docs/getting_started/scala/tutorial.md)
* [Getting started with Kotlin](docs/getting_started/kotlin/tutorial.md)

## Concepts

* [Actor Pattern](docs/concepts/actor/actor_pattern.md)

## How Actions Works

* [Enabling the Actor Pattern](docs/topics/enable_actor_pattern.md)
