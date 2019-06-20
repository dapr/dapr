#!/bin/bash
export GOOS=windows
export GOARCH=amd64
set -eux
mkdir -p ../dist
go build -o action.exe ../cmd/action
go build -o assigner.exe ../cmd/assigner
mv action.exe ../dist
mv assigner.exe ../dist