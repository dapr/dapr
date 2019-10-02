# API-007: Tracing Endpoint

## Status 
Proposed

## Context
We now support distributed tracing across Dapr sidecars, and we inject correlation id to HTTP headers and gRPC metadata before we hand the requests to user code. However, it's up to the user code to configure and implement proper tracing themselves.

## Decisions
We should consider adding a tracing endpoint that user code can call in to log traces and telemetries.