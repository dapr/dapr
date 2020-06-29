## Overview

| packages  | description                                                            |
|-----------|------------------------------------------------------------------------|
| common    | common protos that are imported by multiple packages                   |
| internals | internal gRPC and protobuf definitions which is used for Dapr internal |
| runtime   | Dapr and App Callback services and its associated protobuf messages    |
| operator  | Dapr Operator gRPC service                                             |
| placement | Dapr Placement service                                                 |
| sentry    | Dapr Sentry for CA service                                             |

## Proto client generation

### Prerequsites

Because of [etcd dependency issue](https://github.com/etcd-io/etcd/issues/11563), contributor needs to use the below verisons of tools to generate gRPC protobuf clients.

* protoc version: [v3.11.0](https://github.com/protocolbuffers/protobuf/releases/tag/v3.11.0)
* protobuf protoc-gen-go: [v1.3.2](https://github.com/golang/protobuf/releases/tag/v1.3.2)
* gRPC version: [v1.26.0](https://github.com/grpc/grpc-go/releases/tag/v1.26.0)

### Generate go clients

> TODO: move the commands to makefile

```bash
protoc -I . ./dapr/proto/operator/v1/*.proto --go_out=plugins=grpc:../../../
protoc -I . ./dapr/proto/placement/v1/*.proto --go_out=plugins=grpc:../../../
protoc -I . ./dapr/proto/sentry/v1/*.proto --go_out=plugins=grpc:../../../
protoc -I . ./dapr/proto/common/v1/*.proto --go_out=plugins=grpc:../../../
protoc -I . ./dapr/proto/runtime/v1/*.proto --go_out=plugins=grpc:../../../
protoc -I . ./dapr/proto/internals/v1/*.proto --go_out=plugins=grpc:../../../
```

## Update e2e test apps
Whenever there are breaking changes in the proto files, we need to update the e2e test apps to use the correct version of dapr dependencies. This can be done by navigating to the tests folder and running the commands:-

```
./update_testapps_dependencies.sh
```
**Note**: On Windows, use the mingw tools to execute the bash script

Check in all the go.mod files for the test apps that have now been modified to point to the latest dapr version.