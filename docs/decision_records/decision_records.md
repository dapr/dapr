# Architecture Decision Records

Architecture Decision Records (ADRs or simply decision records) are a collection of records for "architecturally significant" decisions. A decision record is a short markdown file in a specific light-weight format.

This folder contains all the decisions we have recorded in Actions, including Actions runtime, Actions CLI as well as Actions SDKs in different languages.

## Actions decision record organization and index
All decisions are categorized in the following folders:
* **Architecture** - Decisions on general architecture, code structure, coding conventions and common practices.
  
  - [ARC-001: Refactor for modularity and testability](./architecture/ARC-001-refactor-for-modularity-and-testability.md)
  - [ARC-002: Multitenancy](./architecture/ARC-002-multitenancy.md)
  
* **API** - Decisions on Actions runtime API designs.

  - [API-001: State store API design](./api/API-001-state-store-api-design.md)
  - [API-002: Actor API design](./api/API-002-actor-api-design.md)
  - [API-003: Messaging API names](./api/API-003-messaging-api-names.md)
  - [API-004: Binding Manifests](./api/API-004-binding-manifests.md)
  - [API-005: State store behavior](./api/API-005-state-store-behavior.md)
  - 
* **CLI** - Decisions on Actions CLI architecture and behaviors.

  - [CLI-001: CLI and runtim versioning](./cli/CLI-001-cli-and-runtime-versioning.md)
* **SDKs** - Decisions on Actions SDKs.
* **Engineering** - Decisions on Engineering practices, including CI/CD, testings and releases.
* **Images** - Decisions on container image versioning and tagging.

  - [IMAGES-001: Tagging](https://github.com/actionscore/actions/blob/master/docs/decision_records/images/IMAGES-001-tagging.MD)

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
