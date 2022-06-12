module app

go 1.18

require (
	github.com/dapr/dapr v1.7.4
	github.com/golang/protobuf v1.5.2
	github.com/gorilla/mux v1.8.0
	google.golang.org/grpc v1.46.2
	google.golang.org/grpc/examples v0.0.0-20220314230434-84793b56f63c
	google.golang.org/protobuf v1.28.0
)

require (
	github.com/google/uuid v1.3.0 // indirect
	golang.org/x/net v0.0.0-20220520000938-2e3eb7b945c2 // indirect
	golang.org/x/sys v0.0.0-20220412211240-33da011f77ad // indirect
	golang.org/x/text v0.3.7 // indirect
	google.golang.org/genproto v0.0.0-20211104193956-4c6863e31247 // indirect
)

replace github.com/dapr/dapr => ../../../
