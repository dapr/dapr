# API

This folder is intended for the standardizing of errors per API. 

The format of the files related to the standardization of errors is:
```text
<building_block>.go
```

## Usage

Example: 

* in `pubsub.go`
```go
import kitErrors "github.com/dapr/kit/errors"

// Define error in dapr pkg/api/<building_block>.go
func PubSubNotFound(name string, pubsubType string, metadata map[string]string) error {
message := fmt.Sprintf("pubsub %s not found", name)

return kitErrors.NewBuilder(
grpcCodes.InvalidArgument,
http.StatusNotFound,
message,
kitErrors.CodePrefixPubSub+kitErrors.CodeNotFound,
).
WithErrorInfo(kitErrors.CodePrefixPubSub+kitErrors.CodeNotFound, metadata).
WithResourceInfo(pubsubType, name, "", message).
Build()
}
```

* use error in appropriate file, ex: `pkg/grpc/api.go`
```go
import (	
    apiErrors "github.com/dapr/dapr/pkg/api/errors"
    kitErrors "github.com/dapr/kit/errors"
)
// Use error in dapr and pass in relevant information
err = apiErrors.PubSubNotFound(pubsubName, pubsubType, metadata)
```
