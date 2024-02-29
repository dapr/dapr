package diagnostics

import (
	diagUtils "github.com/dapr/dapr/pkg/diagnostics/utils"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

func NewDaprTraceSampler(samplingRateString string) sdktrace.Sampler {
	samplingRate := diagUtils.GetTraceSamplingRate(samplingRateString)
	return sdktrace.ParentBased(sdktrace.TraceIDRatioBased(samplingRate))
}
