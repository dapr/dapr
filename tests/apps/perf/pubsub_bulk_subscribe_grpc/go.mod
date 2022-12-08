module github.com/dapr/dapr/tests/apps/perf/pubsub_bulk_subscribe_grpc

go 1.19

require (
	github.com/dapr/dapr v1.9.5
	github.com/golang/protobuf v1.5.2
	google.golang.org/grpc v1.51.0
	google.golang.org/protobuf v1.28.1
)

require (
	golang.org/x/net v0.1.0 // indirect
	golang.org/x/sys v0.1.0 // indirect
	golang.org/x/text v0.4.0 // indirect
	google.golang.org/genproto v0.0.0-20221027153422-115e99e71e1c // indirect
)

replace github.com/dapr/dapr => ../../../../
