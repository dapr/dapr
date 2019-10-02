package exporters

import (
	"contrib.go.opencensus.io/exporter/zipkin"
	openzipkin "github.com/openzipkin/zipkin-go"
	zipkinHTTP "github.com/openzipkin/zipkin-go/reporter/http"
	"go.opencensus.io/trace"
)

// ZipkinExporter is an OpenCensus zipkin exporter
type ZipkinExporter struct {
}

// Init creates a new zipkin endpoint and reporter
func (z ZipkinExporter) Init(daprID string, hostPort string, exporterAddress string) error {
	localEndpoint, err := openzipkin.NewEndpoint(daprID, hostPort)
	if err != nil {
		return err
	}
	reporter := zipkinHTTP.NewReporter(exporterAddress)
	ze := zipkin.NewExporter(reporter, localEndpoint)
	trace.RegisterExporter(ze)
	trace.ApplyConfig(trace.Config{DefaultSampler: trace.AlwaysSample()})
	return nil
}
