
# Dapr 0.8.0

We're happy to announce the release of Dapr 0.8.0!

**Highlights**


* Added new components: [MQTT Pub/Sub](https://docs.dapr.io/operations/components/setup-pubsub/supported-pubsub/setup-mqtt/), [Apache Pulsar Pub/Sub](https://docs.dapr.io/operations/components/setup-pubsub/supported-pubsub/setup-pulsar/), [AWS SNS/SQS Pub/Sub](https://docs.dapr.io/operations/components/setup-pubsub/supported-pubsub/setup-aws-snssqs/)
* [Custom Pub/Sub routes](https://docs.dapr.io/developing-applications/building-blocks/pubsub/howto-publish-subscribe/#step-2-subscribe-to-topics)
* [Bi-directional bindings](https://docs.dapr.io/developing-applications/building-blocks/bindings/howto-bindings/)
* [Dapr API authentication](https://docs.dapr.io/operations/security/api-token/)
* [W3C trace header support](https://docs.dapr.io/developing-applications/building-blocks/observability/w3c-tracing/)
* [Enhanced mDNS service discovery to support host name](https://github.com/dapr/dapr/issues/1506)
* [Default common components path for Dapr CLI](https://github.com/dapr/cli/issues/340)
* [Added Python Actor SDK](https://github.com/dapr/python-sdk)

We would like to extend our thanks to all new and existing contributors who helped make this release happen.

If you're new to Dapr, visit the [getting started](https://docs.dapr.io/getting-started/) page and familiarize yourself with Dapr.

Docs have been updated with all the new features and changes of this release. To get started with new capabilities introduced in this release, go to the [Concepts](https://docs.dapr.io/concepts/) and the [Developing applications](https://docs.dapr.io/developing-applications/).

> **Note: This release contains a few [breaking changes](#breaking-changes).**

See [this](#upgrading-to-dapr-0.8.0) section on upgrading Dapr to version 0.8.0.

## New in this release

### Dapr Runtime

**Added** custom routes for pub/sub (#breaking-changes)

**Enriched** http and grpc status code conversion.

**Added** Healthz server for control plane.

**Refactored** gRPC service and protobuf message definitions. (#breaking-changes)

**Fixed** Actor activation and method invocation logic to handle app restarts.

**Fixed** Unpopulated metadata for input bindings for HTTP app channel.

**Improved** client trace span generation calls.

**Added** liveness config.

**Added** disable Span Generations when tracing rate is set to 0.

**Added** trace headers in API response, which allows users to correlate with

**Enhanced** mDNS to support hostname and IPs. (Thanks @jjcollinge)

**Added** mqtt pubsub provider to daprd. (Thanks @kumaya)

**Added** Pulsar pubsub options. (Thanks @elyst)

**Added** API Authentication.

**Added** bi-directional bindings support. (#breaking-changes)

**Added** Set Metadata option to state store Get API.

**Added** opentelemetry sementic convention.

**Added** SNS/SQS pubsub provider.

**Added** Context timeout to internal grpc dial.

**Added** Linear interval retry for Internal gRPC client.

**Added** pending actor locks metric.

**Generated** low-cardinality metric method label.

**Fixed** marshalling error for gRPC binary metadata.

**Improved** component folder search behavior by looking at home directory. (#breaking-changes)

**Fixed** log options for contrib logger and enable metrics only for middleware. (Thanks @ksashikumar)

**Fixed** http method label for HTTP appchannel metric.


### Components

**Added** AWS SNS/SQS pubsub. (Thanks @khous)

**Added** Apache Pulsar. (Thanks @elyst)

**Added** MQTT pubsub. (Thanks @kumaya)

**Fixed** Azure Service Bus pubsub throttling. (Thanks @jjcollinge)

**Fixed** SQL server state store integration tests. (Thanks @brooke-hamilton)

**Updated** mDNS resolver to respect service IP address. (Thanks @jjcollinge)

**Fixed** nethttpadaper to handle UserValue in request context. (Thanks @jjcollinge)

**Improved** AWS authentication for dynamodb, kinesis, s3, sns, sqs, and secretmanager. (Thanks @trondhindenes)

**Improved** dynamodb component to use BatchWriteItem for BulkSet and BulkDelete. (Thanks @ksashikumar)

**Added** bi-directional bindings support for all bindings components.


### CLI

**Added** new flag to provide custom component directory path for dapr run. (Thanks @ksashikumar)

**Added** verbose logs on failure of container uninstall and check if docker is installed. (Thanks @ksashikumar)

**Improved** stop command to stop multiple apps. (Thanks @chinzhiweiblank)

**Added** default components directory in user home directory.

**Changed** `-p` option to `-d` to specify the payload for `dapr publish`


### .NET SDK

**Added** Actor proxy method with parameterized interface. (Thanks @jpiquot)

**Added** Bi-directional output binding.

**Updated** Protobuf clients. (Thanks @amolenk)

**Rename** actorImplementationTypeName to actorTypeName. (Thanks @fduman)

**Corrected** the order of middleware chaining.

**Rename** pubsub event `content` to `data`.

**Changed** Actor activation logic to activate it only when actor is invoked.


### Java SDK

**Updated** Protobuf clients.

**Added** Bi-directional output binding.

**Changed** Actor activation logic to activate it only when actor is invoked.

**Rename** pubsub event `param` to `data`.


### Python-SDK

**Added** Actor SDK.

**Updated** Protobuf clients.

**Added** Kubernetes example. (Thanks @XavierGeerinck)

**Added** initial setup for sphinx. (Thanks @chinzhiweiblank)


### JS-SDK / RUST-SDK / CPP-SDK

**Updated** Protobuf clients. (Thanks @gdhuper for rust-sdk update)


### Samples

**Added** new Secrets example.

**Added** new Docker Compose.

**Updated** Distributed Calculator sample to show how to run locally.


### Docs

**Added** Docker-compose howto. (Thanks @jjcollinge)

**Added** docs authoring guideline

**Added** pubsub custom routes

**Added** liveness config annotation

**Updated** actor spec to remove activation api

**Added** how to enable tls for redis cache (Thanks @robinmanuelthiel)

**Added** token-based authentication

**Added** more exampels to Azure Event Grid binding (Thanks @Masahigo)

**Added** bi-directional bindings howto

**Added** mqtt pubsub howto (Thanks @kumaya)

**Updated** dapr run cli to use cli components path

**Added** apache pulsar pubsub howto (Thanks @elyst)

**Added** AWS SNS/SQS pubsub howto (Thanks @khous)

**Updated** missing steps for eventhubs

**Added** use-cases to concept docs (Thanks @arschles)

**Added** W3C trace context howtos

**Added** Grafana Actor Dashboard

**Added** how to use multi fragment path for service invocation


## Tests

**Added** alerts for control plane and daprd for cpu/mem

**Refactor** longhaul tests apps to use shared library


## Upgrading to Dapr 0.8.0

If you're upgrading from an older version of Dapr to 0.8.0, follow the steps here to ensure a smooth upgrade. You know, the one where you don't get red errors on the terminal.. we all hate that, right?

### Local Machine / Self-hosted

Uninstall Dapr using the CLI you currently have:

```bash
dapr uninstall --all
```

Next, get the latest CLI following [these](https://github.com/dapr/cli#installing-dapr-cli) instructions, or alternatively download the latest and greatest release from [here](https://github.com/dapr/cli/releases) and put the `dapr` binary in your PATH.

Once you have downloaded the CLI, run:

```bash
dapr init
```

Wait for the update to finish, and you're good to go!
Make sure you have the right runtime version (0.8.0) with:

```bash
dapr --version
```

### Kubernetes

Download the latest CLI release as outlined above in the `Local Machine / Self-hosted` section.

If you previously installed Dapr on your Kubernetes cluster using the Dapr CLI, run:

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

Alternatively, if you want to install Dapr in a dev/test setup, run:

```
dapr init --kubernetes
```

#### Post installation

After Dapr 0.8.0 has been installed, perform a rolling restart for your deployments.

## Breaking Changes

### Pub Sub Routes

Starting this release, topic subscribers can specify the endpoint route that Dapr should call for a given topic.

Old subscription JSON:

```json
[
  "topic1",
  "topic2"
]
```

New subscription JSON:

```json
[
  {
    "topic": "topic1",
    "route": "custom/topic1"
  },
  {
    "topic": "topic2",
    "route": "custom2/topic2"
  },
]
```

For more details, see [here](https://docs.dapr.io/developing-applications/building-blocks/pubsub/howto-publish-subscribe/#step-2-subscribe-to-topics)

### Bi-directional bindings

Output bindings can now optionally support operations that return data to the caller.
When invoking an output binding, a new required field has been added: `operation`.

Old output binding JSON:

```json
{
  "data": {
    "name": "Java the Hutt"
  }
}
```

New output binding JSON:

```json
{
  "data": {
    "name": "Java the Hutt"
  },
  "operation": ""
}
```

100% of the current bindings support the `create` operation.
As new functionality is introduced to new or existing bindings, the list of supported operations can be found for each binding in the [bindings spec section](https://docs.dapr.io/operations/components/setup-bindings/supported-bindings/) in the docs.

### gRPC Protobuf update

You must upgrade SDKs to the latest SDK to use the latest Dapr

* Package name in Dapr gRPC protobuf files has been changed to :
  - renamed `dapr.proto.dapr.v1` to `dapr.proto.runtime.v1`
  - renamed `dapr.proto.daprclient.v1` to `dapr.proto.runtime.v1`
  - moved the common protobuf message related to service invocation to  `dapr.proto.common.v1`  package


### Dapr CLI publish option change

* payload option for `dapr publish` is changed from `-p` to `-d`.

