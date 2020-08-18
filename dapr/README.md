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

### Getting the correct versions of tools

If you have the versions above, you can skip this section.

#### protoc 

Click the link above, download the appropriate file for your OS and unzip it in a location of your choice.  You can add this to your path if you choose.  If you don't, you'll have to use the full path later.


#### protoc-gen-go/grpc-go

To get the version listed above (1.3.2):

```
cd ~/go/src
mkdir temp
cd temp
go mod init 
go get -d -v github.com/golang/protobuf@v1.3.2
```

Open go.mod and add this line to the end to add the specific version grpc required, including the comment:

```
require google.golang.org/grpc v1.26.0 // indirect
```

go.mod should now look like this:

```
module temp

go 1.14

require github.com/golang/protobuf v1.3.2 // indirect
require google.golang.org/grpc v1.26.0 // indirect
```

Now build:

```
go build github.com/golang/protobuf/protoc-gen-go
```

The binary will be put in current directory.

Copy the binary to your go bin (e.g. ~/go/bin) or some preferred location.  Add that location to your path.

Now generate the protobufs.  For this example assume you have a change in `dapr/dapr/proto/runtime/v1/dapr.proto` and want to generate from that:

```
protoc -I . ./dapr/proto/runtime/v1/*.proto --go_out=plugins=grpc:../../../
```

> Note if you didn't add protoc to your path above, you'll have to use the full path.

The output will be `*pb.go` files in a file hierarchy starting 3 dirs above the current directory.  Find the `*pb.go` files and diff them with the current `dapr/dapr/pkg/proto/runtime/v1/dapr.pb.go`. Assuming you have a small change (e.g. adding a field to a struct), the diff should be relatively small, less than 10 lines, other than to the file descriptor which will look like an array of bytes.  If the size of the diff is much larger than that, the version of tools you're using likely does not match the ones above.

Finally, copy the generated pb.go files over the corresponding ones in the dapr/dapr repo.  In this case, that is `dapr/dapr/pkg/proto/runtime/v1/dapr.pb.go`.

Repeat for each modified `.proto`.

### Generate go clients

> TODO: move the commands to makefile

To generate all protobufs:

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