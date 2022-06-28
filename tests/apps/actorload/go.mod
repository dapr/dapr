module actorload

go 1.18

require (
	fortio.org/fortio v1.6.8
	github.com/go-chi/chi v4.1.2+incompatible
	github.com/google/uuid v1.2.0
	golang.org/x/sys v0.0.0-20210616094352-59db8d763f22 // indirect
	google.golang.org/protobuf v1.26.0 // indirect
)

require (
	go.opentelemetry.io/otel v0.13.0
	go.opentelemetry.io/otel/exporters/metric/prometheus v0.0.0-00010101000000-000000000000
)

require (
	github.com/DataDog/sketches-go v0.0.1 // indirect
	github.com/beorn7/perks v1.0.1 // indirect
	github.com/cespare/xxhash/v2 v2.1.1 // indirect
	github.com/golang/protobuf v1.5.0 // indirect
	github.com/matttproud/golang_protobuf_extensions v1.0.2-0.20181231171920-c182affec369 // indirect
	github.com/prometheus/client_golang v1.9.0 // indirect
	github.com/prometheus/client_model v0.2.0 // indirect
	github.com/prometheus/common v0.15.0 // indirect
	github.com/prometheus/procfs v0.6.0 // indirect
	github.com/stretchr/testify v1.7.0 // indirect
	go.opentelemetry.io/otel/sdk v0.13.0 // indirect
	golang.org/x/xerrors v0.0.0-20200804184101-5ec99f83aff1 // indirect
	gopkg.in/yaml.v3 v3.0.0-20210107192922-496545a6307b // indirect
)

replace (
	go.opentelemetry.io/otel => go.opentelemetry.io/otel v0.13.0
	go.opentelemetry.io/otel/api/global => go.opentelemetry.io/otel/api/global v0.13.0
	go.opentelemetry.io/otel/api/metric => go.opentelemetry.io/otel/api/metric v0.13.0
	go.opentelemetry.io/otel/exporters/metric/prometheus => go.opentelemetry.io/otel/exporters/metric/prometheus v0.13.0
	go.opentelemetry.io/otel/label => go.opentelemetry.io/otel/label v0.13.0
)
