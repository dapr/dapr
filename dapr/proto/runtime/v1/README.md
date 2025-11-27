# Public Dapr Runtime APIs

The `dapr/proto/runtime/v1` folder contains a proto file per the user-facing Dapr APIs.

## Proto client generation

Pre-requisites:
1. Install protoc version: [v4.25.4](https://github.com/protocolbuffers/protobuf/releases/tag/v4.25.4)

2. Install protoc-gen-go, protoc-gen-go-grpc and protoc-gen-connect-go

```bash
make init-proto
```

*If* protoc is already installed:

3. Generate gRPC proto clients from the root of the project

```bash
make gen-proto
```

4. See the auto-generated files in `pkg/proto`
