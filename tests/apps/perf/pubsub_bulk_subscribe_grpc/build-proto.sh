#!/bin/sh

PROTO_PREFIX:=github.com/dapr/dapr/tests/proto/pubsub_bulk_subscribe_grpc

protoc \
  --go_out=. \
  --go_opt=module=$PROTO_PREFIX \
  --go-grpc_out=. \
  --go-grpc_opt=module=$PROTO_PREFIX \
  proto/v1/*.proto