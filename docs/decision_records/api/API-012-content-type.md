# API-012: Content Type

## Status
Rejected

## Context
Adding content-type to state store, pubsub and bindings.

## Decisions

* We will not add content-type since it is a persisted metadata and it can cause problems such as:
  * Long term support since metadata persisted previously would need to be supported indefinetely.
  * Added requirement for components to implement, leading to potentially hacky implementations to persist metadata side-by-side with data.

## Consequences

SDKs need to handle deserialization on their end, requiring enough context in the API to determine how to handle type deserialization.
