# API-002: Actor API design

## Status
Accepted

## Context
Given Dapr is going out with language specific Actor SDKs, we formally introduced an Actor API into Dapr to make Actors are first-class citizen in Dapr. The goal of this review was to ensure Dapr can provide strong support of Service Fabric stateful actors programming model so that we can offer a migration path to the majority of existing actor users.

## Decisions

### Dapr 

* A separate Actor interface is defined.
* Actors should support multiple reminders and timers.
* Actor state access methods are encapsulated in the Actor interface itself.
* Actor interface shall support updating a group of key-value states in a single operation.
* Actor interface shall support deletion of an actor. If the actor is activated when the method is called, the in-flight transaction is allowed to complete, then the actor is deactivated, deleted, with associated state removed. 

### Non-Dapr
* Transaction across multiple API calls is left for future versions, if proven necessary. Due to single-threaded guarantee, such transaction scope might be unnecessary. However, if developer expects an Actor code to behave atomically (in an implied transaction scope), we may have to implement this.

## Consequences

Dapr can provide strong support of Service Fabric stateful actors programming model so that we can offer a migration path to most existing actor users.