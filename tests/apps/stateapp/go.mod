module app

go 1.14

require (
	github.com/dapr/components-contrib v0.2.5-0.20200812172248-3efcb4043035
	github.com/dapr/dapr v0.9.1-0.20200814002316-9d6fad285504
	github.com/gorilla/mux v1.7.3
	google.golang.org/grpc v1.26.0
)

replace k8s.io/client => github.com/kubernetes-client/go v0.0.0-20190928040339-c757968c4c36
