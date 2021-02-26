module app

go 1.16

require (
	github.com/dapr/dapr v1.0.0-rc.2.0.20210122165906-be08e5520173
	github.com/gorilla/mux v1.7.3
	google.golang.org/grpc v1.34.0
)

replace k8s.io/client => github.com/kubernetes-client/go v0.0.0-20190928040339-c757968c4c36
