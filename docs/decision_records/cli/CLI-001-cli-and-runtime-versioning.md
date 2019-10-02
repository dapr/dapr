# CLI-001: CLI and runtime versioning

## Status
Accepted

## Context
As we formally establish Dapr component version, we need to decide if we want to couple CLI versions with runtime versions.

## Decisions

* We'll keep CLI versioning and runtime versioning separate.
* CLI will pull down latest runtime binary during the *init()* command.
* Version scheme is: *major.minor.revision.build* for both CLI and runtime.
  
## Consequences

This allows us each Dapr component to evolve independently.