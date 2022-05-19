module app

go 1.18

require (
	github.com/google/uuid v1.1.2
	github.com/gorilla/mux v1.8.0
	k8s.io/apimachinery v0.20.0
)

replace k8s.io/client => github.com/kubernetes-client/go v0.0.0-20190928040339-c757968c4c36
