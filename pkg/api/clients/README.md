# Dapr Go Clients generated from Open API Spec

To generate Clients from the Open API spec we are using `oapi-codegen`. 
You can install it locally by running: 

```sh
go install github.com/deepmap/oapi-codegen/cmd/oapi-codegen@latest

```

You can now generate the client for the statestore component running: 
```sh
oapi-codegen -generate types,client,spec -package statestore https://raw.githubusercontent.com/dapr/sig-api/main/statestore-api/statestore.yaml > statestore/statestore.gen.go

```

Or the PubSub component: 

```sh
oapi-codegen -generate types,client,spec -package pubsub https://raw.githubusercontent.com/dapr/sig-api/main/pubsub-api/pubsub.yaml > pubsub/pubsub.gen.go
```