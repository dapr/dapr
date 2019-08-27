# API-005: State Store Behavior

## Status
Proposed

## Context
As we continue to solidify our API spec, we need to explicitly define component behaviors in the spec and make sure those are implemented in our implementation. This document captures our decisions on state store behaviors. It's expected that we'll create more of such documents to capture explicit component behavior decisions. 

## Decisions

### Concurrency model

* Actions supports two flavors of optimistic concurrency: first-write wins and last-write wins. First-write wins is implemented through ETag.
* User code can express concurrency intention with a *config* annotation attached to a request. See **Config annotation** for details.
* Future version of Actions may support call throttling through application channel. 

### Consistency model

* Actions supports both eventual consistency and strong consistency. 
* Actors always use strong consistency.

### Transactions

* Actions-compatible state stores shall support ACID transactions.
* Actions doesn't mandate specific transaction isolation level at this point. However, when deemed necessary, we can easily add those to **Config annotation** as needed.

### Config annotation

* User payload can contain an optional **config** annotation/element that expresses various constraints and policies to be applied to the call, including:
  * Concurrency model: first-write or last-write
  * Consistency model: strong or eventual
  * Retry policies:
    * Interval
    * Pattern: linear, expotential
    * Circuit-breaker Timeout (before an open circuit-breaker is reset) 

### State store configuration probe

* An Actions-comptaible state store shall provide an endpoint that answers to configuration probe and returns (among others):
  * Supported concurrency model
  * Supported consistency model
* A state store instance shall return the specific configuration of the current instance.
* It's considered out of scope to require state store to dynamically apply new configurations.
  
### Actions

* Update state store API spec to reflect above decisions
* Create backlog of issues to implement above decisions