# Architecture Decision Records

Architecture Decision Records (ADRs or simply decision records) are a collection of records for "architecturally significant" decisions. A decision record is a short markdown file in a specific light-weight format.

This folder contains all the decisions we have recorded in Dapr, including Dapr runtime, Dapr CLI as well as Dapr SDKs in different languages.

## Dapr decision record organization and index

All decisions are categorized in the following folders:

* **Architecture** - Decisions on general architecture, code structure, coding conventions and common practices.
  
  - [ARC-001: Refactor for modularity and testability](./architecture/ARC-001-refactor-for-modularity-and-testability.md)
  - [ARC-002: Multitenancy](./architecture/ARC-002-multitenancy.md)
  - [ARC-003: gRPC and Protobuf message coding convention](./architecture/ARC-003-grpc-protobuf-coding-convention.md)
  - [ARC-004: HTTP API server](./architecture/ARC-004-http-server.md)
  
* **API** - Decisions on Dapr runtime API designs.

  - [API-001: State store API design](./api/API-001-state-store-api-design.md)
  - [API-002: Actor API design](./api/API-002-actor-api-design.md)
  - [API-003: Messaging API names](./api/API-003-messaging-api-names.md)
  - [API-004: Binding Manifests](./api/API-004-binding-manifests.md)
  - [API-005: State store behavior](./api/API-005-state-store-behavior.md)
  - [API-006: Universal namespace (customer ask)](./api/API-006-universal-namespace.md)
  - [API-007: Tracing Endpoint](./api/API-007-tracing-endpoint.md)
  - [API-008: Multi State store API design](./api/API-008-multi-state-store-api-design.md)
  - [API-009: Bi-Directional Bindings](./api/API-009-bidirectional-bindings.md)
  - [API-010: Appcallback Versioning for HTTP](./api/API-010-appcallback-versioning.md)
  - [API-011: State Store APIs Parity](./api/API-011-state-store-api-parity.md)
  - [API-012: Content Type](./api/API-012-content-type.md)

* **CLI** - Decisions on Dapr CLI architecture and behaviors.

  - [CLI-001: CLI and runtime versioning](./cli/CLI-001-cli-and-runtime-versioning.md)
  - [CLI-002: Self-hosted init and uninstall behaviors](./cli/CLI-002-self-hosted-init-and-uninstall-behaviors.md)
  
* **SDKs** - Decisions on Dapr SDKs.

  - [SDK-001: SDK releases](./sdk/SDK-001-releases.md)
  - [SDK-002: Java JDK versions](./sdk/SDK-002-java-jdk-versions.md)

* **Engineering** - Decisions on Engineering practices, including CI/CD, testing and releases.

  - [ENG-001: Image Tagging](./engineering/ENG-001-tagging.md)
  - [ENG-002: Dapr Release](./engineering/ENG-002-Dapr-Release.md)
  - [ENG-003: Test Infrastructure](./engineering/ENG-003-test-infrastructure.md)
  - [ENG-004: Signing](./engineering/ENG-004-signing.md)

## Creating new decision records

A new decision record should be a _.md_ file named as 
```
<category prefix>-<sequence number in category>-<descriptive title>.md
```
|Category|Prefix|
|----|----|
|Architecture|ARC|
|API|API|
|CLI|CLI|
|SDKs|SDK|
|Engineering|ENG|

A decision record should contain the following fields:

* **Status** - can be "proposed", "accepted", "implemented", or "rejected".
* **Context** - the context of the design discussion.
* **Decision** - Description of the decision.
* **Consequences** - what impacts this decision may create.
* **Implementation** - when a decision is implemented, the corresponding doc should be updated with the following information (when applicable):
  * Release version
  * Associated test cases
