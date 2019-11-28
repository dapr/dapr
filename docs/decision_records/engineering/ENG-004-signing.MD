# ENG-004: Binary Signing

## Status
Accepted

## Context
Authenticode signing of binaries.

## Decisions

* Binaries will not be signed with Microsoft keys. In future we can revisit to sign the binaries with dapr.io keys.
  
## Consequences

This will allow the Dapr releases to be built outside of Microsoft build and release pipelines.
