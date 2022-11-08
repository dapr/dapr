package diagnostics

import (
	"fmt"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"

	diagUtils "github.com/dapr/dapr/pkg/diagnostics/utils"
)

type DaprTraceSampler struct {
	sdktrace.Sampler
	ProbabilitySampler sdktrace.Sampler
	SamplingRate       float64
}

/**
 * Decisions for the Dapr sampler are as follows:
 *  - parent has sample flag turned on -> sample
 *  - parent has sample turned off -> fall back to probability-based sampling
 */
func (d *DaprTraceSampler) ShouldSample(p sdktrace.SamplingParameters) sdktrace.SamplingResult {
	psc := trace.SpanContextFromContext(p.ParentContext)
	if psc.IsValid() && psc.IsSampled() {
		// Parent is valid and specifies sampling flag is on -> sample
		return sdktrace.AlwaysSample().ShouldSample(p)
	}

	// Parent is invalid or does not have sampling enabled -> sample probabilistically
	return d.ProbabilitySampler.ShouldSample(p)
}

func (d *DaprTraceSampler) Description() string {
	return fmt.Sprintf("DaprTraceSampler(P=%f)", d.SamplingRate)
}

func NewDaprTraceSampler(samplingRateString string) *DaprTraceSampler {
	samplingRate := diagUtils.GetTraceSamplingRate(samplingRateString)
	return &DaprTraceSampler{
		SamplingRate:       samplingRate,
		ProbabilitySampler: sdktrace.TraceIDRatioBased(samplingRate),
	}
}
