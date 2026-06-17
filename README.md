<div style="text-align: center"><img src="/img/dapr_logo.svg" height="120px">
<h2>The runtime for durable execution, AI agents, and secure distributed applications</h2>
</div>

[![Go Report][go-report-badge]][go-report-url] [![OpenSSF][openssf-badge]][openssf-url] [![Docker Pulls][docker-badge]][docker-url] [![Build Status][actions-badge]][actions-url] [![Test Status][e2e-badge]][e2e-url] [![Code Coverage][codecov-badge]][codecov-url] [![License: Apache 2.0][apache-badge]][apache-url] [![FOSSA Status][fossa-badge]][fossa-url] [![TODOs][todo-badge]][todo-url] [![Good First Issues][gfi-badge]][gfi-url] [![discord][discord-badge]][discord-url] [![YouTube][youtube-badge]][youtube-link] [![Bluesky][bluesky-badge]][bluesky-link] [![X/Twitter][x-badge]][x-link]

[go-report-badge]: https://goreportcard.com/badge/github.com/dapr/dapr
[go-report-url]: https://goreportcard.com/report/github.com/dapr/dapr
[openssf-badge]: https://www.bestpractices.dev/projects/5044/badge
[openssf-url]: https://www.bestpractices.dev/projects/5044
[docker-badge]: https://img.shields.io/docker/pulls/daprio/daprd?style=flat&logo=docker
[docker-url]: https://hub.docker.com/r/daprio/dapr
[apache-badge]: https://img.shields.io/github/license/dapr/dapr?style=flat&label=License&logo=github
[apache-url]: https://github.com/dapr/dapr/blob/master/LICENSE
[actions-badge]: https://github.com/dapr/dapr/workflows/dapr/badge.svg?event=push&branch=master
[actions-url]: https://github.com/dapr/dapr/actions?workflow=dapr
[e2e-badge]: https://img.shields.io/endpoint?url=https://gist.githubusercontent.com/dapr-bot/14e974e8fd6c6eab03a2475beb1d547a/raw/dapr-test-badge.json
[e2e-url]: https://github.com/dapr/dapr/actions?workflow=dapr-test&event=schedule
[codecov-badge]: https://codecov.io/gh/dapr/dapr/branch/master/graph/badge.svg
[codecov-url]: https://codecov.io/gh/dapr/dapr
[fossa-badge]: https://app.fossa.com/api/projects/custom%2B162%2Fgithub.com%2Fdapr%2Fdapr.svg?type=shield
[fossa-url]: https://app.fossa.com/projects/custom%2B162%2Fgithub.com%2Fdapr%2Fdapr?ref=badge_shield
[todo-badge]: https://badgen.net/https/api.tickgit.com/badgen/github.com/dapr/dapr
[todo-url]: https://www.tickgit.com/browse?repo=github.com/dapr/dapr
[gfi-badge]:https://img.shields.io/github/issues-search/dapr/dapr?query=type%3Aissue%20is%3Aopen%20label%3A%22good%20first%20issue%22&label=Good%20first%20issues&style=flat&logo=github
[gfi-url]:https://github.com/dapr/dapr/issues?q=is%3Aissue+is%3Aopen+label%3A%22good+first+issue%22
[discord-badge]: https://img.shields.io/discord/778680217417809931?label=Discord&style=flat&logo=discord
[discord-url]: http://bit.ly/dapr-discord
[youtube-badge]:https://img.shields.io/youtube/channel/views/UCtpSQ9BLB_3EXdWAUQYwnRA?style=flat&label=YouTube%20views&logo=youtube
[youtube-link]:https://youtube.com/@daprdev
[bluesky-badge]:https://img.shields.io/badge/Follow-%40daprdev.bsky.social-0056A1?logo=bluesky
[bluesky-link]:https://bsky.app/profile/daprdev.bsky.social
[x-badge]:https://img.shields.io/twitter/follow/daprdev?logo=x&style=flat
[x-link]:https://twitter.com/daprdev

Dapr is an open-source runtime for building distributed applications, workflows, and AI agents. It provides **durable execution**, secure service-to-service communication, state management, event-driven messaging, and a consistent set of APIs that run anywhere — on Kubernetes, in the cloud, at the edge, or on your laptop.

Whether you're building AI agents, business-critical workflows, microservices, or event-driven systems, Dapr lets you focus on business logic instead of plumbing. Dapr runs as a sidecar, so every capability is available from *any* language over standard HTTP and gRPC, with native SDKs for **.NET, Java, Python, Go, JavaScript/TypeScript, and Rust** — no framework lock-in and no boilerplate to reach production-ready reliability, security, and observability.

![Dapr overview](./img/overview.png)

## Why Dapr?

Modern applications are no longer simple request/response services. They are long-running workflows, AI agents, event-driven pipelines, multi-agent systems, and human-in-the-loop processes — and they must survive failures, maintain state, and communicate securely in production.

Building all of that reliably is hard. Dapr brings proven patterns and battle-tested building blocks together into one consistent programming model, so you don't have to reinvent durability, security, or messaging for every service you ship. You're never exposed to low-level primitives like threading, partitioning, or retry logic — and you can move seamlessly between platforms and backing infrastructure without rewriting your code. Platform teams use Dapr to provide governance and golden paths, while application teams get simple APIs that work the same everywhere.

## Durable Execution

Applications fail. Infrastructure restarts. Networks partition. Your workflows and agents shouldn't lose their progress when that happens.

**Dapr Workflows** provide durable execution that automatically persists progress and resumes from the last completed step after:

- Process crashes
- Pod restarts
- Node failures
- Rolling deployments
- Infrastructure interruptions

Instead of restarting from the beginning, execution picks up exactly where it left off — with no extra database or state-machine code to write. You author workflows as ordinary code in your language of choice; if the application crashes midway through, Dapr recovers automatically and resumes from the next unfinished step.

**Common use cases:** AI agents, customer onboarding, order processing, human-approval flows, document processing, and other multi-step business processes.

## Build Reliable AI Agents

AI agents need far more than model inference. To run in production they need state, orchestration, recovery, secure communication, and governance.

Dapr provides the runtime primitives to operate agents reliably — and works alongside your existing AI frameworks and models:

- **Durable agent execution** that survives crashes and restarts
- **Long-running, multi-step tasks** backed by Workflows
- **Multi-agent orchestration** and agent-to-agent communication
- **State persistence** and memory across interactions
- **LLM integration** through the Conversation API, with prompt caching and tool calling
- **Human-in-the-loop approvals** and event-driven coordination
- **Automatic recovery** after failures

## Secure by Default

Every Dapr application is issued a cryptographically verifiable identity. Security is built in, not bolted on:

- Mutual TLS (mTLS) for all service-to-service traffic
- Workload identity and authentication
- Fine-grained, least-privilege authorization policies
- Secret management backed by your preferred vault
- Automatic certificate issuance and rotation
- Zero-trust communication

Control exactly which applications, agents, and services are allowed to talk to each other:

```yaml
apiVersion: dapr.io/v1alpha1
kind: Configuration
metadata:
  name: access-policy
spec:
  accessControl:
    defaultAction: deny
    policies:
      - appId: orders
        defaultAction: allow
```

Apply least-privilege access controls across your entire environment, without writing custom security infrastructure.

## Verifiable Execution

For many systems, knowing that work *completed* isn't enough — you need proof of *how* it completed.

Building on Dapr's cryptographic workload identity and durable workflow history, verifiable execution provides evidence of:

- Where work originated and which identity executed it
- The sequence and lineage of execution
- The authenticity of inputs and outputs
- Whether execution history was tampered with

This enables auditable workflows and AI systems that can prove integrity, authenticity, and provenance — ideal for financial services, healthcare, government, and other compliance-sensitive environments.

## Distributed Application APIs

Durable execution and AI build on the same proven building blocks that power Dapr microservices. Mix and match what you need:

| API | What it does |
|:----|:-------------|
| **Workflows** | Author long-running, durable workflows and agentic processes as code |
| **Service Invocation** | Reliable, secure service-to-service calls with built-in mTLS, retries, and observability |
| **State Management** | Persist and query state across dozens of stores without coupling to a database |
| **Pub/Sub** | Build event-driven systems on your preferred message broker with at-least-once delivery |
| **Actors** | Build stateful, virtual actor-based applications |
| **Conversation** | Call LLMs through a consistent API, with prompt caching and tool calling |
| **Bindings** | Trigger your code from, and send events to, external systems |
| **Secrets** | Retrieve secrets securely from external secret stores |
| **Configuration** | Read and subscribe to application configuration consistently |
| **Distributed Lock** | Coordinate access to shared resources safely |
| **Cryptography** | Encrypt and decrypt data without exposing keys to your application |
| **Jobs** | Schedule work to run now or in the future |

All APIs are available over HTTP and gRPC, with SDKs for Java, .NET, Go, JavaScript, Python, Rust, C++, and PHP. You benefit from built-in [observability](https://docs.dapr.io/concepts/observability-concept/), reliability, and pluggable, vendor-neutral components for state stores, message brokers, and more across Azure, AWS, and GCP.

## Run Anywhere

Dapr runs as a sidecar (container or process) next to your app and works identically across:

- Kubernetes
- AWS, Azure, and Google Cloud
- Virtual machines and bare metal
- Edge and IoT environments
- Air-gapped deployments

It's lightweight (a ~58MB binary using ~4MB of memory), supports every programming language over HTTP and gRPC, and requires no application code changes to move between platforms. Adopt it incrementally, one API at a time, with no runtime lock-in.

## Trusted in Production

Dapr is a **graduated** Cloud Native Computing Foundation (CNCF) project, used by organizations around the world to build mission-critical applications. Teams choose Dapr to recover automatically from failures, build reliable AI systems, secure communication between services, simplify distributed architectures, and avoid infrastructure lock-in.

<p align="center"><img src="https://raw.githubusercontent.com/kedacore/keda/main/images/logo-cncf.svg" height="75px"></p>

## Get Started

See our [Getting Started](https://docs.dapr.io/getting-started/) guide over in our docs.

## Quickstarts and Samples

* See the [quickstarts repository](https://github.com/dapr/quickstarts) for code examples that can help you get started with Dapr.
* Explore additional samples in the Dapr [samples repository](https://github.com/dapr/samples).

## Community
We want your contributions and suggestions! One of the easiest ways to contribute is to participate in discussions on the mailing list, chat on IM or the bi-weekly community calls.
For more information on the community engagement, developer and contributing guidelines and more, head over to the [Dapr community repo](https://github.com/dapr/community#dapr-community).

### Contact Us

Reach out with any questions you may have and we'll make sure to answer them as soon as possible!

| Platform  | Link        |
|:----------|:------------|
| 💬 Discord (preferred) | [![Discord Banner](https://discord.com/api/guilds/778680217417809931/widget.png?style=banner2)](https://aka.ms/dapr-discord)
| 💭 LinkedIn | [@daprdev](https://www.linkedin.com/company/daprdev)
| 🦋 BlueSky | [@daprdev.bsky.social](https://bsky.app/profile/daprdev.bsky.social)
| 🐤 Twitter | [@daprdev](https://twitter.com/daprdev)


### Community Call

Every two weeks we host a community call to showcase new features, review upcoming milestones, and engage in a Q&A. All are welcome!

📞 Visit [Upcoming Dapr Community Calls](https://github.com/dapr/community/issues?q=is%3Aissue%20state%3Aopen%20label%3A%22community%20call%22) for upcoming dates and the meeting link.

📺 Visit https://www.youtube.com/@DaprDev/streams for previous community call live streams.

### Videos and Podcasts

We have a variety of keynotes, podcasts, and presentations available to reference and learn from.

📺 Visit https://docs.dapr.io/contributing/presentations/ for previous talks and slide decks or our YouTube channel https://www.youtube.com/@DaprDev/videos.

### Contributing to Dapr

See the [Development Guide](https://docs.dapr.io/contributing/) to get started with building and developing.

## Repositories

| Repo | Description |
|:-----|:------------|
| [Dapr](https://github.com/dapr/dapr) | The main repository that you are currently in. Contains the Dapr runtime code and overview documentation.
| [CLI](https://github.com/dapr/cli) | The Dapr CLI allows you to setup Dapr on your local dev machine or on a Kubernetes cluster, provides debugging support, launches and manages Dapr instances.
| [Docs](https://docs.dapr.io) | The documentation for Dapr.
| [Quickstarts](https://github.com/dapr/quickstarts) | This repository contains a series of simple code samples that highlight the main Dapr capabilities.
| [Samples](https://github.com/dapr/samples) | This repository holds community maintained samples for various Dapr use cases.
| [Components-contrib ](https://github.com/dapr/components-contrib) | The purpose of components contrib is to provide open, community driven reusable components for building distributed applications.
| [Go-sdk](https://github.com/dapr/go-sdk) | Dapr SDK for Go
| [Java-sdk](https://github.com/dapr/java-sdk) | Dapr SDK for Java
| [JS-sdk](https://github.com/dapr/js-sdk) | Dapr SDK for JavaScript
| [Python-sdk](https://github.com/dapr/python-sdk) | Dapr SDK for Python
| [Dotnet-sdk](https://github.com/dapr/dotnet-sdk) | Dapr SDK for .NET
| [Rust-sdk](https://github.com/dapr/rust-sdk) | Dapr SDK for Rust
| [Cpp-sdk](https://github.com/dapr/cpp-sdk) | Dapr SDK for C++
| [PHP-sdk](https://github.com/dapr/php-sdk) | Dapr SDK for PHP


## Code of Conduct

Please refer to our [Dapr Community Code of Conduct](https://github.com/dapr/community/blob/master/CODE-OF-CONDUCT.md)
