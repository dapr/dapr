module app

go 1.15

require (
	github.com/dapr/dapr v0.11.1-0.20201016010930-cf4e080c1a54
	github.com/golang/protobuf v1.3.3
	github.com/google/uuid v1.1.1
	github.com/gorilla/mux v1.7.3
	google.golang.org/grpc v1.26.0
)

replace k8s.io/client => github.com/kubernetes-client/go v0.0.0-20190928040339-c757968c4c36
