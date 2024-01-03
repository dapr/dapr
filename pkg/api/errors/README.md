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
import kiterrors "github.com/dapr/kit/errors"

// Define error in dapr pkg/api/<building_block>.go
func PubSubNotFound(name string, pubsubType string, metadata map[string]string) error {
message := fmt.Sprintf("pubsub %s not found", name)

return kiterrors.NewBuilder(
grpcCodes.InvalidArgument,
http.StatusNotFound,
message,
kiterrors.CodePrefixPubSub+kiterrors.CodeNotFound,
).
WithErrorInfo(kiterrors.CodePrefixPubSub+kiterrors.CodeNotFound, metadata).
WithResourceInfo(pubsubType, name, "", message).
Build()
}
```

* use error in appropriate file, ex: `pkg/grpc/api.go`
```go
import (	
    apierrors "github.com/dapr/dapr/pkg/api/errors"
    kiterrors "github.com/dapr/kit/errors"
)
// Use error in dapr and pass in relevant information
err = apierrors.PubSubNotFound(pubsubName, pubsubType, metadata)
```
