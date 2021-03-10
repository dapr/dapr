module app

go 1.16

require (
	github.com/gorilla/mux v1.7.3
	k8s.io/apimachinery v0.20.0
)

replace k8s.io/client => github.com/kubernetes-client/go v0.0.0-20190928040339-c757968c4c36
