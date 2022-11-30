#!/bin/sh

PROTO_PREFIX:=github.com/dapr/dapr/tests/apps/perf/pubsub_subscribe_grpc/pkg/proto/v1

protoc \
  --go_out=. \
  --go_opt=module=$(PROTO_PREFIX) \
  --go-grpc_out=. \
  --go-grpc_opt=module=$(PROTO_PREFIX) \
  proto/v1/*.proto