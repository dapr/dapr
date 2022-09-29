module github.com/dapr/dapr/tests/apps/resiliencyapp_grpc

go 1.19

require (
	github.com/dapr/dapr v1.7.4
	google.golang.org/grpc v1.48.0
	google.golang.org/grpc/examples v0.0.0-20220818173707-97cb7b1653d7
	google.golang.org/protobuf v1.28.0
)

require (
	github.com/golang/protobuf v1.5.2 // indirect
	golang.org/x/net v0.0.0-20220927171203-f486391704dc // indirect
	golang.org/x/sys v0.0.0-20220928140112-f11e5e49a4ec // indirect
	golang.org/x/text v0.3.7 // indirect
	google.golang.org/genproto v0.0.0-20220622171453-ea41d75dfa0f // indirect
)

replace github.com/dapr/dapr => ../../../
