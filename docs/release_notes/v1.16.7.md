# Dapr 1.16.7

This update includes bug fixes:

- [Trace information not populated in pubsub component using GPRC and raw payloads](#trace-information-not-populated-in-pubsub-component-using-gprc-and-raw-payloads)
- [Pulsar pubsub component fails to initialize when using oauth2ClientSecretPath](#pulsar-pubsub-component-fails-to-initialize-when-using-oauth2clientsecretpath)

## Trace information not populated in pubsub component using GPRC and raw payloads

### Problem

The pubsub component did not correctly propagate tracing information when delivering messages over gRPC using raw payloads.

### Impact

Distributed traces were incomplete or missing links between publishers and subscribers. This prevented users from reliably correlating pubsub messages with their originating requests and spans.

### Root Cause

The trace context from the incoming gRPC call was not correctly applied to the context used for the pubsub publish call, especially for raw payloads. As a result, the outgoing pubsub publish did not carry the expected tracing headers/metadata, so downstream components and tracing backends could not associate the published message with the originating span, leading to missing or broken traces for gRPC-based pubsub flows.

### Solution

For raw payload publishes, the same trace information is now written directly into the pubsub metadata that accompanies the raw message, ensuring that tracing works consistently regardless of payload mode.

## Pulsar pubsub component fails to initialize when using oauth2ClientSecretPath

### Problem

Pulsar Pubsub component `oauth2ClientSecretPath` configuration looked for a token and not a client secret or json credentials.

### Impact

Applications using the Pulsar pubsub component with OAuth2 client credentials stored on disk via `oauth2ClientSecretPath` could not start successfully. 

### Root Cause

`oauth2ClientSecretPath` used an incorrect method NewAuthenticationTokenFromFile which looked for a token and not a client secret.

### Solution

`oauth2ClientSecretPath` configuration is now corrected to read a client secret from file. A new option, `oauth2CredentialsFile`, has been added to load `client_id`, `client_secret`, `issuer_url` , and from a JSON file with the format: `{"client_id": "...", "client_secret": "...", "issuer_url": "..."}`.