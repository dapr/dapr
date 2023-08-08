# Dapr Go Clients generated from Open API Spec

To generate Clients from the Open API spec we are using `oapi-codegen`. 
You can install it locally by running: 

```
go install github.com/deepmap/oapi-codegen/cmd/oapi-codegen@latest

```

You can now generate the clients running: 
```
oapi-codegen -generate types,client,spec -package statestore https://raw.githubusercontent.com/dapr/sig-api/main/statestore-api/statestore.yaml > statestore/statestore.gen.go

```