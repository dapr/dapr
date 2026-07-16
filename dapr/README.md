## Overview

| packages   | description                                                            |
| ---------- | ---------------------------------------------------------------------- |
| common     | common protos that are imported by multiple packages                   |
| internals  | internal gRPC and protobuf definitions which is used for Dapr internal |
| runtime    | Dapr and App Callback services and its associated protobuf messages    |
| operator   | Dapr Operator gRPC service                                             |
| placement  | Dapr Placement service                                                 |
| sentry     | Dapr Sentry for CA service                                             |
| components | Dapr gRPC-based components services                                    |

## Proto client generation

1. Install protoc version: [v25.4](https://github.com/protocolbuffers/protobuf/releases/tag/v25.4)

2. Install protoc-gen-go, protoc-gen-go-grpc and protoc-gen-connect-go

```bash
make init-proto
```

3. Generate gRPC proto clients

```bash
make gen-proto
```

### Instructions on Windows (WSL2 with Ubuntu 24.04)
Use the following variation of the steps above

1. Install protoc version: [v25.4](https://github.com/protocolbuffers/protobuf/releases/tag/v25.4) with the following 
from your Ubuntu terminal to download the protoc package locally, unzip and copy into /usr/bin/protoc, update your
$PATH and delete the local temp directory.
```bash
sudo apt install unzip
cd ~
mkdir protoc
cd protoc
# Assumes 32- or 64-bit - replace as necessary
wget https://github.com/protocolbuffers/protobuf/releases/download/v25.4/protoc-25.4-linux-x86_64.zip
unzip protoc-25.4-linux-x64_64.zip
rm protoc-25.4-linux-x64_64.zip
sudo mv bin/* /usr/local/bin
sudo mv include/* /usr/local/include
cd ..
sudo rm -r protoc
```
2. Navigate to your Dapr repository. For example, if you've got it in Windows at `P:/Code/Dapr` in your Ubuntu terminal
use `cd /mnt/p/Code/Dapr`.
3. Install proto-gen-go, protoc-gen-go-grpc and protoc-gen-connect-go, then move from their install directory to your previously created directory
in `/usr/bin/protoc`
```bash
make init-proto
cp ~/go/bin
ln -s ~/go/bin/protoc-gen-go /usr/local/bin
ln -s ~/go/bin/proto-gen-go-grpc /usr/local/bin
ln -s ~/go/bin/proto-gen-connect-go /usr/local/bin
```
4. Generate the proto clients
```bash
make gen-proto
```


## Update e2e test apps

Whenever there are breaking changes in the proto files, we need to update the e2e test apps to use the correct version of dapr dependencies. This can be done by navigating to the tests folder and running the commands:-

```
# Use the last commit of dapr.
./update_testapps_dependencies.sh be08e5520173beb93e5d5f047dbde405e78db658
```

**Note**: On Windows, use the mingw tools to execute the bash script

Check in all the go.mod files for the test apps that have now been modified to point to the latest dapr version.
