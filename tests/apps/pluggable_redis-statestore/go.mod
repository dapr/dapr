module github.com/dapr/dapr/tests/apps/pluggable_redis-statestore

go 1.19

replace github.com/dapr/dapr => ../../../

require (
	github.com/dapr-sandbox/components-go-sdk v0.0.0-20221003123222-b1e4882f892e
	github.com/dapr/components-contrib v1.9.0-rc.1
	github.com/dapr/kit v0.0.3-0.20220930182601-272e358ba6a7
)

require (
	github.com/agrea/ptr v0.0.0-20180711073057-77a518d99b7b // indirect
	github.com/cespare/xxhash/v2 v2.1.2 // indirect
	github.com/dapr/dapr v1.9.0-rc.1 // indirect
	github.com/dgryski/go-rendezvous v0.0.0-20200823014737-9f7001d12a5f // indirect
	github.com/go-redis/redis/v8 v8.11.5 // indirect
	github.com/golang/protobuf v1.5.2 // indirect
	github.com/google/uuid v1.3.0 // indirect
	github.com/json-iterator/go v1.1.12 // indirect
	github.com/mitchellh/mapstructure v1.5.1-0.20220423185008-bf980b35cac4 // indirect
	github.com/modern-go/concurrent v0.0.0-20180306012644-bacd9c7ef1dd // indirect
	github.com/modern-go/reflect2 v1.0.2 // indirect
	github.com/pkg/errors v0.9.1 // indirect
	github.com/sirupsen/logrus v1.9.0 // indirect
	golang.org/x/net v0.0.0-20220927171203-f486391704dc // indirect
	golang.org/x/sys v0.0.0-20220928140112-f11e5e49a4ec // indirect
	golang.org/x/text v0.3.7 // indirect
	google.golang.org/genproto v0.0.0-20220929141241-1ce7b20da813 // indirect
	google.golang.org/grpc v1.49.0 // indirect
	google.golang.org/protobuf v1.28.1 // indirect
)
