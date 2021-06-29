# ARC-001: Refactor for modularity and testability

## Status
Accepted

## Context
As we keep building up Dapr features, it becomes apparent that we need to refactor the existing code base to reinforce component modularity. This will improve testability and maintainability in long run. And this refactor also lays the foundation of opening up extensible points (such as Bindings) to the community.

## Decisions

### Dapr
* Formally separate hosting and API implementations. Hosting provides communication protocols (HTTP/gRPC) as different access heads to the same Dapr API implementation.
* Ensure consistency between gRPC and HTTP interface.
* Separate binding implementations to a separate repository. 
* Use smart defaults for configurable parameters.
* Rename Dapr runtime binary from **dapr** to **daprd**.

### Non-Dapr
* We may consider allowing Dapr to dynamically load bindings during runtime. However, we are not going to implement this unless it's justified by customer asks.
* A unified configuration file that includes paths to individual configuration files.
* Provide a Discovery building block with hopefully pluggable discovery mechanisms (such as a custom DNS).

## Consequences

This will improve testability and maintainability in long run.