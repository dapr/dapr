# Internal APIs

This folder is intended for the Internal APIs that the `daprd` sidecars use to communicate with each other.

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