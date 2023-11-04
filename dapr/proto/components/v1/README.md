# Pluggable Components APIs

This folder is intended for Pluggable Components. 

These protos are for a feature of Dapr which allows users to write their own components that sit outside of the Daprd binary. For more details about pluggable components please refer to the [docs](https://docs.dapr.io/developing-applications/develop-components/pluggable-components/develop-pluggable/).

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