# Dapr - Any language, any framework, anywhere

Dapr is a portable, event-driven, serverless runtime for building distributed applications across cloud and edge.

[![Go Report Card](https://goreportcard.com/badge/github.com/dapr/dapr)](https://goreportcard.com/report/github.com/dapr/dapr)
[![Build Status](https://dev.azure.com/azure-octo/Dapr/_apis/build/status/builds/dapr%20build?branchName=master)](https://dev.azure.com/azure-octo/Dapr/_build/latest?definitionId=5&branchName=master)
[![Gitter](https://badges.gitter.im/Dapr/community.svg)](https://gitter.im/Dapr/community?utm_source=badge&utm_medium=badge&utm_campaign=pr-badge)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://opensource.org/licenses/MIT)

- [dapr.io](https://dapr.io)
- [@DaprDev](https://twitter.com/DaprDev)
- [gitter.im/Dapr](https://gitter.im/Dapr/community)

__Note: Dapr is currently under community development in alpha phase. Dapr is not expected to be used for production workloads until its 1.0 stable release.__

Dapr is a portable, event-driven runtime that makes it easy for developers to build resilient, stateless and stateful microservices that run on the cloud and edge and embraces the diversity of languages and developer frameworks.

Dapr codifies the *best practices* for building microservice applications into open, independent, building blocks that enable you to build portable applications with the language and framework of your choice. Each building block is independent and you can use one, some, or all of them in your application.

![Dapr Conceptual Model](/img/dapr_conceptual_model.jpg)

## Goals

- Enable developers using *any* language or framework to write distributed applications
- Solve the hard problems developers face building microservice applications by providing best practice building blocks
- Be community driven, open and vendor neutral, seeking new contributors
- Provide consistency and portability through open APIs
- Be platform agnostic across cloud and edge
- Embrace extensibility and provide pluggable components without vendor lock-in
- Enable IoT and edge scenarios by being highly performant and lightweight
- Be incrementally adoptable from existing code, with no runtime dependency

## How it works

Dapr injects a side-car container/process to each compute unit. The side-car interacts with event triggers and communicates with the compute unit via standard HTTP or gRPC protocols. This enables Dapr to support all existing and future programming languages without requiring you to import frameworks or libraries.

Dapr offers built-in state management, reliable messaging (at least once delivery), triggers and bindings through standard HTTP verbs or gRPC interfaces. This allows you to write stateless, stateful and actor-like services following the same programming paradigm. You can freely choose consistency model, threading model and message delivery patterns.

Dapr runs natively on Kubernetes, as a standalone binary on your machine, on an IoT device, or as a container that can be injected into any system, in the cloud or on-premises.

Dapr uses pluggable state stores and message buses such as Redis as well as gRPC to offer a wide range of communication methods, including direct dapr-to-dapr using gRPC and async Pub-Sub with guaranteed delivery and at-least-once semantics.


## Why Dapr?

Writing high performance, scalable and reliable distributed application is hard. Dapr brings proven patterns and practices to you. It unifies event-driven and actors semantics into a simple, consistent programming model. It supports all programming languages without framework lock-in. You are not exposed to low-level primitives such as threading, concurrency control, partitioning and scaling. Instead, you can write your code by implementing a simple web server using familiar web frameworks of your choice.

Dapr is flexible in threading and state consistency models. You can leverage multi-threading if you choose to, and you can choose among different consistency models. This flexibility enables to implement advanced scenarios without artificial constraints. You might also choose to utilize single-threaded calls familiar in other Actor frameworks. Dapr is unique because you can transition seamlessly between these models without rewriting your code. 

## Features

* Event-driven Pub-Sub system with pluggable providers and at-least-once semantics
* Input and Output bindings with pluggable providers
* State management with pluggable data stores
* Consistent service-to-service discovery and invocation
* Opt-in stateful models: Strong/Eventual consistency, First-write/Last-write wins
* Cross platform Virtual Actors
* Rate limiting
* Built-in distributed tracing using Open Telemetry
* Runs natively on Kubernetes using a dedicated Operator and CRDs
* Supports all programming languages via HTTP and gRPC
* Multi-Cloud, open components (bindings, pub-sub, state) from Azure, AWS, GCP
* Runs anywhere - as a process or containerized
* Lightweight (58MB binary, 4MB physical memory)
* Runs as a sidecar - removes the need for special SDKs or libraries
* Dedicated CLI - developer friendly experience with easy debugging
* Clients for Java, Dotnet, Go, Javascript and Python

## Get Started using Dapr

See [Getting Started](https://github.com/dapr/docs/tree/master/getting-started).

## Samples

* [Run Dapr Locally](https://github.com/dapr/samples/tree/master/1.hello-world)
* [Run Dapr in Kubernetes](https://github.com/dapr/samples/tree/master/2.hello-kubernetes)

See [Samples](https://github.com/dapr/samples) for additional samples.

## Repositories

| Repo | Description |
|:-----|:------------|
| [Dapr](https://github.com/dapr/dapr) | The main repository that you are currently in. Contains the Dapr runtime code and overview documentation.
| [CLI](https://github.com/dapr/cli) | The Dapr CLI allows you to setup Dapr on your local dev machine or on a Kubernetes cluster, provides debugging support, launches and manages Dapr instances.
| [Docs](https://github.com/dapr/docs) | The documentation repository for Dapr.
| [Samples](https://github.com/dapr/samples) | This repository contains a series of samples that highlight Dapr capabilities.
| [Components-contrib ](https://github.com/dapr/components-contrib) | The purpose of components contrib is to provide open, community driven reusable components for building distributed applications. 
| [Go-sdk](https://github.com/dapr/go-sdk) | Dapr SDK for Go
| [Java-sdk](https://github.com/dapr/java-sdk) | Dapr SDK for Java
| [JS-sdk](https://github.com/dapr/js-sdk) | Dapr SDK for JavaScript
| [Python-sdk](https://github.com/dapr/python-sdk) | Dapr SDK for Python
| [Dotnet-sdk](https://github.com/dapr/dotnet-sdk) | Dapr SDK for .NET Core


## Roadmap

See [Roadmap](https://github.com/dapr/dapr/wiki/Roadmap) for what's planned for the Dapr project.

## Contributing to Dapr

See the [Wiki](https://github.com/dapr/dapr/wiki) for information on contributing to Dapr.

See the [Development Guide](https://github.com/dapr/dapr/blob/master/docs/development) to get started with building and developing.

## Code of Conduct

 This project has adopted the [Microsoft Open Source Code of conduct](https://opensource.microsoft.com/codeofconduct/).
 For more information see the [Code of Conduct FAQ](https://opensource.microsoft.com/codeofconduct/faq/) or contact [opencode@microsoft.com](mailto:opencode@microsoft.com) with any additional questions or comments.
