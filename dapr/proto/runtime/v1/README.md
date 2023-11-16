# Public Dapr Runtime APIs

This folder is intended for user-facing APIs. 

- `dapr.proto` is used by the services implemented by the Dapr runtime. Apps calling into the Dapr runtime use these services.
- `appcallback.proto` is for services implemented by apps to receive messages from the Dapr runtime.

## Proto client generation

Pre-requisites:
1. Install protoc version: [v3.21.12](https://github.com/protocolbuffers/protobuf/releases/tag/v21.12) (from protobuf release 21.12)

2. Install protoc-gen-go and protoc-gen-go-grpc

```bash
make init-proto
```

*If* protoc is already installed:

3. Generate gRPC proto clients from the root of the project

```bash
make gen-proto
```

4. See the auto-generated files in `pkg/proto`