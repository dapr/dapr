## Required tool versions to generate gRPC protobuf client

Because of [etcd dependency issue](https://github.com/etcd-io/etcd/issues/11563), contributor needs to use the below verisons of tools to generate gRPC protobuf clients.

* protoc version: [v3.11.0](https://github.com/protocolbuffers/protobuf/releases/tag/v3.11.0)
* protobuf protoc-gen-go: [v1.3.2](https://github.com/golang/protobuf/releases/tag/v1.3.2)
* gRPC version: [v1.26.0](https://github.com/grpc/grpc-go/releases/tag/v1.26.0)

## Generate clients

> Note: TODO - move commands to makefile

```bash
protoc -I . ./pkg/proto/operator/v1/*.proto --go_out=plugins=grpc:../../../
protoc -I . ./pkg/proto/placement/v1/*.proto --go_out=plugins=grpc:../../../
protoc -I . ./pkg/proto/sentry/v1/*.proto --go_out=plugins=grpc:../../../
protoc -I . ./pkg/proto/common/v1/*.proto --go_out=plugins=grpc:../../../
protoc -I . ./pkg/proto/dapr/v1/*.proto --go_out=plugins=grpc:../../../
protoc -I . ./pkg/proto/daprclient/v1/*.proto --go_out=plugins=grpc:../../../
protoc -I . ./pkg/proto/daprinternal/v1/*.proto --go_out=plugins=grpc:../../../
```
