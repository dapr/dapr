module app

go 1.16

require (
	github.com/dapr/dapr v0.11.3
	github.com/golang/protobuf v1.4.3
	github.com/gorilla/mux v1.7.3
	google.golang.org/api v0.39.0
	google.golang.org/grpc v1.34.0
)

replace k8s.io/client => github.com/kubernetes-client/go v0.0.0-20190928040339-c757968c4c36
