module app

go 1.14

require (
	github.com/dapr/dapr v0.9.1-0.20200812210155-e3f5ee0b162d
	sigs.k8s.io/structured-merge-diff/v3 v3.0.0-20200116222232-67a7b8c61874 // indirect
)

replace k8s.io/client => github.com/kubernetes-client/go v0.0.0-20190928040339-c757968c4c36
