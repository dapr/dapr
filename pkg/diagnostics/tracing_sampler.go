package diagnostics

import (
	sdktrace "go.opentelemetry.io/otel/sdk/trace"

	diagUtils "github.com/dapr/dapr/pkg/diagnostics/utils"
)

func NewDaprTraceSampler(samplingRateString string) sdktrace.Sampler {
	samplingRate := diagUtils.GetTraceSamplingRate(samplingRateString)
	return sdktrace.ParentBased(sdktrace.TraceIDRatioBased(samplingRate))
}
