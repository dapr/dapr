# Dapr 1.16.5

This update includes bug fixes:

- [Trace information not populated in pubsub component using GPRC as transport](#trace-information-not-populated-in-pubsub-component-using-gprc-as-transport)
- [Allow for OIDC clientSecret to be rotated when token is refreshed](#allow-for-oidc-clientsecret-to-be-rotated-when-token-is-refreshed-in-the-pulsar-pubsub-component)

## Trace information not populated in pubsub component using GPRC as transport

### Problem

The pubsub component did not correctly propagate tracing information when delivering messages over gRPC.

### Impact

Distributed traces were incomplete or missing links between publishers and subscribers. This prevented users from reliably correlating pubsub messages with their originating requests and spans.

### Root Cause

The gRPC metadata used for pubsub calls did not include the tracing headers expected by downstream services and OpenTelemetry tooling. In particular, the trace context was not consistently attached to outgoing gRPC calls.

### Solution

The trace context is now explicitly added to the outgoing gRPC metadata for pubsub calls. This ensures that downstream services receive the necessary tracing information and that spans can be correctly correlated across pubsub message flows.

## Allow for OIDC clientSecret to be rotated when token is refreshed in the Pulsar PubSub component

### Problem

The Pulsar OAuth2 client in the Go SDK only loads the client secret once at startup, and the Dapr Pulsar component only supports providing the clientSecret as a static value. 
This combination prevents rotating the OAuth2 client secret via a file path and breaks authentication when the clientSecret is changed.

### Impact

Environments with strict security policies that require periodic rotation of the Pulsar OAuth2 client secret cannot safely rotate secrets. 
Once the clientSecret file is updated, token refresh operations may fail because the running client continues using the old secret, leading to authentication errors and potential message flow interruption.

### Root Cause

The Dapr Pulsar component exposes clientSecret only as a literal value in metadata, not as a file path, so it cannot take advantage of secret rotation mechanisms based on files.

### Solution

The Dapr Pulsar component will add support for specifying clientSecret (privateKey) via a file path in its metadata.
