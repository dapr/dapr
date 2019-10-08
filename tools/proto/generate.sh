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

    mkdir -p ${top_root}/../dapr-${language}
    
    for proto_file in "daprclient/daprclient.proto" "dapr/dapr.proto"; do
        echo "Generating ${language} for ${proto_file}"
        ${root}/bin/protoc --proto_path ${top_root}/pkg/proto/ \
            --${2}_out=${top_root}/../dapr-${language} \
            ${top_root}/pkg/proto/${proto_file}
    done
}

# Setup the directories
root=$(dirname "${BASH_SOURCE[0]}")
top_root=${root}/../..

# TODO: Support non-linux here
file="protoc-${VERSION}-linux-x86_64.zip"

# Download and install tools.
wget "https://github.com/protocolbuffers/protobuf/releases/download/v${VERSION}/${file}" \
  -O ${root}/${file}

unzip ${root}/${file} -d ${root}

# generate javascript
generate javascript js
# generate java
generate java java
# generate dotnet
generate dotnet csharp
# generate python
generate python python

# cleanup
rm -r ${root}/include ${root}/bin ${root}/${file} ${root}/readme.txt
