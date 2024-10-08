# Operator Service APIs

This folder is intended for the `operator` service APIs that manage the component updates and provides Kubernetes services endpoints for Dapr. For more details about the `operator` service please refer to the [docs](https://docs.dapr.io/concepts/dapr-services/operator/).

## Proto client generation

Pre-requisites:
1. Install protoc version: [v4.24.4](https://github.com/protocolbuffers/protobuf/releases/tag/v4.24.4)

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
