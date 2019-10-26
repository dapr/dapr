# Tracing Exporters

Tracing Exporters are [OpenTelemetry](https://opentelemetry.io/) exporter wrappers for Dapr. The actual export implementations should be contributed to the [OpenTelemetry repository](https://github.com/open-telemetry/opentelemetry-collector/tree/master/exporter).

Currently supported exporters are:

* Native

  OpenTelemetry default exporter

* String
  
  Export to a string buffer. This is mostly used for testing purposes.

* Zipkin
  
  Export to a [Zipkin](https://zipkin.io/) back-end.

## Implementing a new Exporter wrapper

A compliant exporter wrapper needs to implement one interface: `Exporter`, which has a single method:

```go
type Exporter interface {
	Init(daprID string, hostAddress string, metadata Metadata) error
}
```

The implementation should configure the exporter according to the settings specified in the metadata.  
