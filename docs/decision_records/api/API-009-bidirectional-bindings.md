# API-009: Bi-Directional Bindings

## Status
Accepted

## Context
As we want to provide bi-directional capabilities for bindings to allow for cases such as getting a blob from a storage account,
An API change is needed to account for the requested type of operation.

## Decisions

### Naming

It was decided to keep the bindings name as is. Alternative proposals were included changing bindings to connectors, but a strong case couldn't be made in favor of connectors to justify the breaking change it would cause.

### Types

It was decided to keep the same YAML format for both input bindings and bi-directional bindings as it is today.
After careful inspection, splitting to two types (for example, trigger bindings and bindings) would incur significant maintanace overhead for the app operator and
Did not provide meaningful value.

In addition, there was no feedback from community or prospecive users that input bindings and output bindings were confusing in any way.

### API structure

It was decided that the API url will be kept as: `http://localhost:<port>/v1.0/bindings/<name>`.
The verb for the HTTP API will remain POST/PUT, and the type of operation will be part of a versioned, structured schema for bindings.

This is not a breaking change.

### Schema and versioning

In accordance with our decision to work towards enterprise versioning, it was accepted that schemas will include a `version` field in
The payload to specify which version of given component needs to be used that corresponds to the given payload.

In addition, an extra field will be added to denote the type of operation that binding supports, for example: `get`, `list`, `create` etc.
Bindings components will provide the means for the Dapr runtime to query for their supported capabilities and return a validaton error if the operation type is not supported.
