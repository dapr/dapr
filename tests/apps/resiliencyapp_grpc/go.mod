module app

go 1.18

require (
	github.com/dapr/dapr v1.7.4
	google.golang.org/grpc v1.47.0
	google.golang.org/grpc/examples v0.0.0-20220314230434-84793b56f63c
	google.golang.org/protobuf v1.28.0
)

require (
	github.com/golang/protobuf v1.5.2 // indirect
	golang.org/x/net v0.0.0-20220621193019-9d032be2e588 // indirect
	golang.org/x/sys v0.0.0-20220520151302-bc2c85ada10a // indirect
	golang.org/x/text v0.3.7 // indirect
	google.golang.org/genproto v0.0.0-20220622171453-ea41d75dfa0f // indirect
)

replace github.com/dapr/dapr => ../../../
