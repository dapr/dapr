<div style="text-align: center"><img src="/img/dapr_logo.svg" height="120px">
<h2>Any language, any framework, anywhere</h2>
</div>

[![Go Report Card](https://goreportcard.com/badge/github.com/dapr/dapr)](https://goreportcard.com/report/github.com/dapr/dapr)
[![Build Status](https://github.com/dapr/dapr/workflows/dapr/badge.svg?event=push&branch=master)](https://github.com/dapr/dapr/actions?workflow=dapr)
[![Gitter](https://badges.gitter.im/Dapr/community.svg)](https://gitter.im/Dapr/community?utm_source=badge&utm_medium=badge&utm_campaign=pr-badge)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://opensource.org/licenses/MIT)
[![TODOs](https://badgen.net/https/api.tickgit.com/badgen/github.com/dapr/dapr)](https://www.tickgit.com/browse?repo=github.com/dapr/dapr)
[![Follow on Twitter](https://img.shields.io/twitter/follow/daprdev.svg?style=social&logo=twitter)](https://twitter.com/intent/follow?screen_name=daprdev)

Dapr is a portable, serverless, event-driven runtime that makes it easy for developers to build resilient, stateless and stateful microservices that run on the cloud and edge and embraces the diversity of languages and developer frameworks.

Dapr codifies the *best practices* for building microservice applications into open, independent, building blocks that enable you to build portable applications with the language and framework of your choice. Each building block is independent and you can use one, some, or all of them in your application.

__Note: Dapr is currently under community development in alpha phase. Dapr is not recommended for production workloads until the 1.0 stable release.__

![Dapr overview](https://github.com/dapr/docs/blob/master/images/overview.png)

## Goals

- Enable developers using *any* language or framework to write distributed applications
- Solve the hard problems developers face building microservice applications by providing best practice building blocks
- Be community driven, open and vendor neutral
- Gain new contributors
- Provide consistency and portability through open APIs
- Be platform agnostic across cloud and edge
- Embrace extensibility and provide pluggable components without vendor lock-in
- Enable IoT and edge scenarios by being highly performant and lightweight
- Be incrementally adoptable from existing code, with no runtime dependency

## How it works

Dapr injects a side-car (container or process) to each compute unit. The side-car interacts with event triggers and communicates with the compute unit via standard HTTP or gRPC protocols. This enables Dapr to support all existing and future programming languages without requiring you to import frameworks or libraries.

Dapr offers built-in state management, reliable messaging (at least once delivery), triggers and bindings through standard HTTP verbs or gRPC interfaces. This allows you to write stateless, stateful and actor-like services following the same programming paradigm. You can freely choose consistency model, threading model and message delivery patterns.

Dapr runs natively on Kubernetes, as a self hosted binary on your machine, on an IoT device, or as a container that can be injected into any system, in the cloud or on-premises.

Dapr uses pluggable component state stores and message buses such as Redis as well as gRPC to offer a wide range of communication methods, including direct dapr-to-dapr using gRPC and async Pub-Sub with guaranteed delivery and at-least-once semantics.


## Why Dapr?

Writing high performance, scalable and reliable distributed application is hard. Dapr brings proven patterns and practices to you. It unifies event-driven and actors semantics into a simple, consistent programming model. It supports all programming languages without framework lock-in. You are not exposed to low-level primitives such as threading, concurrency control, partitioning and scaling. Instead, you can write your code by implementing a simple web server using familiar web frameworks of your choice.

Dapr is flexible in threading and state consistency models. You can leverage multi-threading if you choose to, and you can choose among different consistency models. This flexibility enables to implement advanced scenarios without artificial constraints. Dapr is unique because you can transition seamlessly between platforms and underlying implementations without rewriting your code.

## Features

* Event-driven Pub-Sub system with pluggable providers and at-least-once semantics
* Input and output bindings with pluggable providers
* State management with pluggable data stores
* Consistent service-to-service discovery and invocation
* Opt-in stateful models: Strong/Eventual consistency, First-write/Last-write wins
* Cross platform virtual actors
* Secrets management to retrieve secrets from secure key vaults
* Rate limiting
* Built-in [Observability](https://github.com/dapr/docs/tree/master/concepts/observability) support
* Runs natively on Kubernetes using a dedicated Operator and CRDs
* Supports all programming languages via HTTP and gRPC
* Multi-Cloud, open components (bindings, pub-sub, state) from Azure, AWS, GCP
* Runs anywhere, as a process or containerized
* Lightweight (58MB binary, 4MB physical memory)
* Runs as a sidecar - removes the need for special SDKs or libraries
* Dedicated CLI - developer friendly experience with easy debugging
* Clients for Java, .NET Core, Go, Javascript, Python, Rust and C++

## Get Started using Dapr

See [Getting Started](https://github.com/dapr/docs/tree/master/getting-started).

## Samples

* [Run Dapr locally](https://github.com/dapr/samples/tree/master/1.hello-world)
* [Run Dapr in Kubernetes](https://github.com/dapr/samples/tree/master/2.hello-kubernetes)

See [Samples](https://github.com/dapr/samples) for additional samples.

## Community
We want your contributions and suggestions. One of the easiest ways to contribute is to participate in discussions on the mailing list, chat on IM or the bi-weekly community calls. Here is how to get involved.

| Engagement |  |
|:-----|:------------|
| IM chat  | https://gitter.im/Dapr/community 
| Mailing list | https://groups.google.com/forum/#!forum/dapr-dev
| Meeting dates |  [Bi-weekly Tuesdays 10:00AM PST](https://calendar.google.com/calendar?cid=OGQ0ZWNva2xrbHE1YXQ4ZGNsMjg1M2pzbzRAZ3JvdXAuY2FsZW5kYXIuZ29vZ2xlLmNvbQ). Dates for 2020  <br>- 17th March, 31st March <br>- 14th April, 28th April<br>- 12th May, 26th May
| Meeting link | https://aka.ms/dapr-community-call
| Meeting notes | https://aka.ms/dapr-meeting-notes
| Meeting recordings | http://aka.ms/dapr-recordings
| Twitter | [@daprdev](https://twitter.com/daprdev)
| Channel 9 | Azure Friday - Learn All About Distributed Application Runtime Dapr: [Part 1](https://channel9.msdn.com/Shows/Azure-Friday/Learn-all-about-Distributed-Application-Runtime-Dapr-Part-1) and [Part 2](https://channel9.msdn.com/Shows/Azure-Friday/Learn-all-about-Distributed-Application-Runtime-Dapr-Part-2)
| MS Ignite 2019 | [THR2267 - Mark Russinovich presents "Next generation app development and deployment"](https://myignite.techcommunity.microsoft.com/sessions/84599) and [Mark Russinovich presents "The Future of Cloud Native Applications with OAM and Dapr"](https://myignite.techcommunity.microsoft.com/sessions/82059)
| Hanselminutes | [Dapr Distributed Application Runtime with Mark Russinovich](https://hanselminutes.com/718/dapr-distributed-application-runtime-with-azure-cto-mark-russinovich) 
| Azure Community Live | [Build microservice applications using DAPR with Mark Fussell ](https://www.youtube.com/watch?v=CgqI7nen-Ng) 

### Contributing to Dapr

See the [Wiki](https://github.com/dapr/dapr/wiki) for information on contributing to Dapr.

See the [Development Guide](https://github.com/dapr/dapr/blob/master/docs/development) to get started with building and developing.

## Roadmap

See [Roadmap](https://github.com/dapr/dapr/wiki/Roadmap) for what's planned for the Dapr project.

## Repositories

| Repo | Description |
|:-----|:------------|
| [Dapr](https://github.com/dapr/dapr) | The main repository that you are currently in. Contains the Dapr runtime code and overview documentation.
| [CLI](https://github.com/dapr/cli) | The Dapr CLI allows you to setup Dapr on your local dev machine or on a Kubernetes cluster, provides debugging support, launches and manages Dapr instances.
| [Docs](https://github.com/dapr/docs) | The documentation repository for Dapr.
| [Samples](https://github.com/dapr/samples) | This repository contains a series of samples that highlight Dapr capabilities.
| [Components-contrib ](https://github.com/dapr/components-contrib) | The purpose of components contrib is to provide open, community driven reusable components for building distributed applications. 
| [Dashboard ](https://github.com/dapr/dashboard) | General purpose dashboard for Dapr 
| [Go-sdk](https://github.com/dapr/go-sdk) | Dapr SDK for Go
| [Java-sdk](https://github.com/dapr/java-sdk) | Dapr SDK for Java
| [JS-sdk](https://github.com/dapr/js-sdk) | Dapr SDK for JavaScript
| [Python-sdk](https://github.com/dapr/python-sdk) | Dapr SDK for Python
| [Dotnet-sdk](https://github.com/dapr/dotnet-sdk) | Dapr SDK for .NET Core
| [Rust-sdk](https://github.com/dapr/rust-sdk) | Dapr SDK for Rust
| [Cpp-sdk](https://github.com/dapr/cpp-sdk) | Dapr SDK for C++


## Code of Conduct

 This project has adopted the [Microsoft Open Source Code of conduct](https://opensource.microsoft.com/codeofconduct/).
 For more information see the [Code of Conduct FAQ](https://opensource.microsoft.com/codeofconduct/faq/) or contact [opencode@microsoft.com](mailto:opencode@microsoft.com) with any additional questions or comments.
