module github.com/dapr/dapr/tests/apps/pluggable_redis-pubsub

go 1.19

replace github.com/dapr/dapr => ../../../

require (
	github.com/dapr-sandbox/components-go-sdk v0.0.0-20221020133829-d48efa38c091
	github.com/dapr/components-contrib v1.9.1-0.20221020020823-038f63d30938
	github.com/dapr/kit v0.0.3-0.20221009070203-ca4d40d89ed5
)

require (
	github.com/cespare/xxhash/v2 v2.1.2 // indirect
	github.com/dapr/dapr v1.9.0 // indirect
	github.com/dgryski/go-rendezvous v0.0.0-20200823014737-9f7001d12a5f // indirect
	github.com/go-redis/redis/v8 v8.11.5 // indirect
	github.com/golang/protobuf v1.5.2 // indirect
	github.com/google/uuid v1.3.0 // indirect
	github.com/mitchellh/mapstructure v1.5.1-0.20220423185008-bf980b35cac4 // indirect
	github.com/pkg/errors v0.9.1 // indirect
	github.com/sirupsen/logrus v1.9.0 // indirect
	golang.org/x/net v0.1.0 // indirect
	golang.org/x/sys v0.1.0 // indirect
	golang.org/x/text v0.4.0 // indirect
	google.golang.org/genproto v0.0.0-20221018160656-63c7b68cfc55 // indirect
	google.golang.org/grpc v1.50.1 // indirect
	google.golang.org/protobuf v1.28.1 // indirect
)
