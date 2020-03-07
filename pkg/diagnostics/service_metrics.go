package diagnostics

import (
	"context"

	"go.opencensus.io/stats"
	"go.opencensus.io/stats/view"
	"go.opencensus.io/tag"
)

// ServiceMetrics holds dapr runtime metric monitoring methods
type ServiceMetrics struct {
	componentLoaded        *stats.Int64Measure
	componentInitCompleted *stats.Int64Measure
	componentInitFailed    *stats.Int64Measure

	// Metrics Tags
	appIDTag          tag.Key
	componentTag      tag.Key
	compFailReasonTag tag.Key

	ctx context.Context
}

func newView(measure stats.Measure, keys []tag.Key, aggregation *view.Aggregation) *view.View {
	return &view.View{
		Name:        measure.Name(),
		Description: measure.Description(),
		Measure:     measure,
		TagKeys:     keys,
		Aggregation: aggregation,
	}
}

// NewServiceMetrics returns ServiceMetrics instance with default service metric6 stats
func NewServiceMetrics() *ServiceMetrics {
	appIDTag, _ := tag.NewKey("app_id")
	componentTag, _ := tag.NewKey("component")
	compFailReasonTag, _ := tag.NewKey("reason")

	return &ServiceMetrics{
		// Runtime Component metrics
		componentLoaded: stats.Int64(
			"runtime/component/loaded", "The number of successfully loaded components",
			stats.UnitDimensionless),
		componentInitCompleted: stats.Int64(
			"runtime/component/init_total", "The number of initialized components",
			stats.UnitDimensionless),
		componentInitFailed: stats.Int64(
			"runtime/component/init_fail_total", "The number of component initialization failures",
			stats.UnitDimensionless),

		appIDTag:          appIDTag,
		componentTag:      componentTag,
		compFailReasonTag: compFailReasonTag,
		ctx:               context.Background(),
	}
}

// Init initialize metrics views for metrics
func (s *ServiceMetrics) Init(appID string) error {
	s.ctx, _ = tag.New(s.ctx, tag.Insert(s.appIDTag, appID))

	err := view.Register(
		newView(s.componentLoaded, []tag.Key{s.appIDTag}, view.Count()),
		newView(s.componentInitCompleted, []tag.Key{s.appIDTag, s.componentTag}, view.Count()),
		newView(s.componentInitFailed, []tag.Key{s.appIDTag, s.componentTag, s.compFailReasonTag}, view.Count()),
	)

	return err
}

// ComponentLoaded records metric when component is loaded successfully
func (s *ServiceMetrics) ComponentLoaded() {
	stats.Record(s.ctx, s.componentLoaded.M(1))
}

// ComponentInitialized records metric when component is initialized
func (s *ServiceMetrics) ComponentInitialized(component string) {
	stats.RecordWithTags(
		s.ctx,
		[]tag.Mutator{tag.Upsert(s.componentTag, component)},
		s.componentInitCompleted.M(1))
}

// ComponentInitFailed records metric when component initialization is failed
func (s *ServiceMetrics) ComponentInitFailed(component string, reason string) {
	stats.RecordWithTags(
		s.ctx,
		[]tag.Mutator{tag.Upsert(s.componentTag, component), tag.Upsert(s.compFailReasonTag, reason)},
		s.componentInitFailed.M(1))
}
