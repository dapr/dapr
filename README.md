# Dapr - Any language, any framework, anywhere

Dapr is a portable, event-driven, serverless runtime for building distributed applications across cloud and edge.

[![Build Status](https://dev.azure.com/azure-octo/Dapr/_apis/build/status/builds/dapr%20build?branchName=master)](https://dev.azure.com/azure-octo/Dapr/_build/latest?definitionId=5&branchName=master)

- [dapr.io](https://dapr.io)
- [@DaprDev](https://twitter.com/DaprDev)
- [gitter.im/Dapr](https://gitter.im/Dapr/community)

__Note: Dapr is currently under community development in alpha phase. Dapr is not expected to be used for production workloads until its 1.0 stable release.__

Dapr is a programming model runtime for writing cloud-native applications which are distributed, dynamically scaled, and loosely coupled in nature. Dapr offers an eventing system on which compute units communicate with each other by exchanging messages.

![Dapr Conceptual Model](/img/dapr_conceptual_model.png)

- Enables easy, event-driven, stateless and stateful, microservices development
- Is community driven, open source and vendor neutral
- Provides consistency and portability through standard open APIs
- Is platform agnostic across cloud and edge 
- Is incrementally adoptable from existing code, with no runtime dependency 
- Works with any programming language and any developer framework
- Has pluggable bindings to state, messaging and storage systems

Dapr injects a side-car container/process to each compute unit. The side-car interacts with event triggers and communicates with the compute unit via standard HTTP or GRPC protocols. This enables Dapr to support all existing and future programming languages without requiring developers to import frameworks or libraries.

Dapr offers built-in state management, reliable messaging (at least once delivery), triggers and bindings through standard HTTP verbs or GRPC interfaces. This allows developers to write stateless, stateful and actor-like services following the same programming paradigm. Developers can freely choose consistency model, threading model and message delivery patterns.

Dapr runs natively on Kubernetes, as a standalone binary on a developer's machine, on an IoT device, or as a container than can be injected into any system, in the cloud or on-premises.

Dapr uses pluggable state stores and message buses such as Redis as well as gRPC to offer a wide range of communication methods, including direct dapr-to-dapr using gRPC and async Pub-Sub with guaranteed delivery and at-least-once semantics.

The Dapr runtime is designed for hyper-scale performance in the cloud and on the edge.

###### Dapr Standalone Deployment

![Dapr Standalone](/img/dapr_standalone.jpg)

###### Dapr Kubernetes Deployment

![Dapr on Kubernetes](/img/dapr_k8s.jpg)

## Why Dapr?

Writing high performance, scalable and reliable distributed application is hard. Dapr brings proven patterns and practices to every developer. It unifies event-driven and actors semantics into a simple, consistent programming model. It supports all programming languages without framework lock-in. Developers are not exposed to low-level primitives such as threading, concurrency control, partitioning and scaling. Instead, they can write their code by implementing a simple web server using familiar web frameworks of their choice.

Dapr is flexible in threading and state consistency models. Developers can leverage multi-threading if they choose to, and they can choose among different consistency models. This flexibility enables power developers to implement advanced scenarios without artificial constraints. A developer might also choose to utilize single-threaded calls familiar in other Actor frameworks. Dapr is unique because developers can transition seemlessly between these models without rewriting their code. 

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

## Get Started using Dapr

See [Getting Started](https://github.com/dapr/docs/getting-started/readme.md).

## Samples

* [Run Dapr Locally](https://github.com/dapr/samples/tree/master/1.hello-world)
* [Run Dapr in Kubernetes](https://github.com/dapr/samples/tree/master/2.hello-kubernetes)

See [Samples](https://github.com/dapr/samples) for additional samples.

## Roadmap

See [Roadmap](https://github.com/dapr/dapr/wiki/Roadmap) for what's planned for the Dapr project.

## Contributing to Dapr

See the [Wiki](https://github.com/dapr/dapr/wiki) for information on contributing to Dapr.

See the [Development Guide](https://github.com/dapr/dapr/blob/master/docs/development/development.md) to get started with building and developing.

## Code of Conduct

 This project has adopted the [Microsoft Open Source Code of conduct](https://opensource.microsoft.com/codeofconduct/).
 For more information see the [Code of Conduct FAQ](https://opensource.microsoft.com/codeofconduct/faq/) or contact [opencode@microsoft.com](mailto:opencode@microsoft.com) with any additional questions or comments.