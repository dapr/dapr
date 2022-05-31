# API-012: Content Type

## Status
Accepted

## Context
Not adding content-type to state store, pubsub and bindings.

## Decisions

* We will not add content-type since it is a persisted metadata and it can cause problems such as:
  * Long term support since metadata persisted previously would need to be supported indefinitely.
  * Added requirement for components to implement, leading to potentially hacky implementations to persist metadata side-by-side with data.

Original issue and discussion: https://github.com/dapr/dapr/issues/2026

## Consequences

SDKs need to handle deserialization on their end, requiring enough context in the API to determine how to handle type deserialization.
