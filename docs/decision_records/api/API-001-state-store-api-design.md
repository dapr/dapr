# API-001: State store API design

## Status
Accepted

## Context
We reviewed storage API design for completeness and consistency.

## Decisions

* All requests/responses use a single parameter that represents the request/response object. This allows us to extend/update request/response object without changing the API.
* Add Delete() method
* Support bulk operations: BulkDelete() and BulkSet(). All operations in the bulk are expected to be completed within a single transaction scope.
* Support a generic BulkOperation() method, which is carried out as a single transaction.
* Transaction across multiple API requests is postponed to future versions.
* Actor state operations are moved to a new Actor interface. Please see [API-002-actor-api-design](./API-002-actor-api-design.md).

## Consequences

With these changes we should meet stateful actor requirements.