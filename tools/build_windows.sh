#!/bin/bash
export GOOS=windows
export GOARCH=amd64
set -eux
mkdir -p ../dist
go build -o controller ../cmd/controller
go build -o action ../cmd/action
go build -o assigner ../cmd/assigner
mv controller ../dist
mv action ../dist
mv assigner ../dist