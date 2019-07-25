package exporters

import (
	"contrib.go.opencensus.io/exporter/zipkin"
	openzipkin "github.com/openzipkin/zipkin-go"
	zipkinHTTP "github.com/openzipkin/zipkin-go/reporter/http"
	"go.opencensus.io/trace"
)

type ZipkinExporter struct {
}

func (z ZipkinExporter) Init(action_id string, action_address string, exporter_address string) error {
	localEndpoint, err := openzipkin.NewEndpoint(action_id, action_address)
	if err != nil {
		return err
	}
	reporter := zipkinHTTP.NewReporter(exporter_address)
	ze := zipkin.NewExporter(reporter, localEndpoint)
	trace.RegisterExporter(ze)
	trace.ApplyConfig(trace.Config{DefaultSampler: trace.AlwaysSample()})
	return nil
}
