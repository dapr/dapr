# ARC-003: gRPC and Protobuf message coding convention

## Status

Proposed

## Context

We have defined gRPC services and protobuf messages without convention, which results in the duplicated protobuf definitions and inconsistent names of services and messages. Thus, this record defines the minimum-level coding convention for Protobuf message to improve the quality of grpc/protobuf message definitions.

## Decisions

* Use `google.protobuf.Any` data field only if the message field conveys serialized protobuf message with type url. Otherwise, use the explicit data type or protobuf message.
* Use `Request` suffix for gRPC request message name and `Response` suffix for gRPC response message name
* Do not use `Client` and `Service` suffix for gRPC service name e.g. (x) DaprClient, DaprService
* Avoid the duplicated protobuf message definitions by defining the messages in shared proto
* Define and use enum type if field accepts only predefined values.

## Consequences

This allows us to define the consistent, readable gRPC service and protobuf message.
