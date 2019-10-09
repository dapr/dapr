# ------------------------------------------------------------
# Copyright (c) Microsoft Corporation.
# Licensed under the MIT License.
# ------------------------------------------------------------

#!/bin/bash

set +x

VERSION=3.10.0

# TODO: Consider using a Docker image for this?

# Generate proto buffers
# First arg is name of language (e.g. 'javascript')
# Second arg is name of tool (e.g. 'js')
generate() {
    language=${1}
    tool=${2}
    path=${3}
    args=${4}

    mkdir -p ${top_root}/../dapr-${language}/${path}
    
    for proto_file in "daprclient/daprclient.proto" "dapr/dapr.proto"; do
        echo "Generating ${language} for ${proto_file}"
        ${root}/bin/protoc --proto_path ${top_root}/pkg/proto/ \
            --${2}_out=${args}:${top_root}/../dapr-${language}/${path} \
            ${top_root}/pkg/proto/${proto_file} ${@:5}
    done
}

# Setup the directories
root=$(dirname "${BASH_SOURCE[0]}")
top_root=${root}/../..

# Detect OS
OS=""
if [[ "$OSTYPE" == "linux-gnu" ]]; then
        OS="linux"
elif [[ "$OSTYPE" == "darwin"* ]]; then
        OS="osx"
fi

file="protoc-${VERSION}-${OS}-x86_64.zip"

# Download and install tools.
wget "https://github.com/protocolbuffers/protobuf/releases/download/v${VERSION}/${file}" \
  -O ${root}/${file}

unzip ${root}/${file} -d ${root}

# find grpc_tools_node_protoc_plugin location
PROTOC_PLUGIN=$(which grpc_tools_node_protoc_plugin)

# generate javascript
generate javascript js src 'import_style=commonjs' '--plugin=protoc-gen-grpc='${PROTOC_PLUGIN} --grpc_out=${top_root}/../dapr-javascript/src

# generate java
generate java java src/main/java '' --grpc-java_out=${top_root}/../dapr-java/src/main/java 

# generate dotnet
# dotnet generates their own via dotnet build...

# generate python
echo 'Generating python for all protos'
mkdir -p ${top_root}/../dapr-python/src
python3 -m grpc.tools.protoc -I${top_root}/pkg/proto \
   --python_out=${top_root}/../dapr-python/src \
   --grpc_python_out=${top_root}/../dapr-python/src \
   dapr/dapr.proto \
   daprclient/daprclient.proto

# cleanup
rm -r ${root}/include ${root}/bin ${root}/${file} ${root}/readme.txt
