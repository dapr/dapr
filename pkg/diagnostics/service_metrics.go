package diagnostics

import (
	"context"

	"go.opencensus.io/stats"
	"go.opencensus.io/stats/view"
	"go.opencensus.io/tag"
)

// ServiceMetrics holds dapr runtime metric monitoring methods
type ServiceMetrics struct {
	// component metrics
	componentLoaded        *stats.Int64Measure
	componentInitCompleted *stats.Int64Measure
	componentInitFailed    *stats.Int64Measure

	// mTLS metrics
	mtlsInitCompleted             *stats.Int64Measure
	mtlsInitFailed                *stats.Int64Measure
	mtlsWorkloadCertRotated       *stats.Int64Measure
	mtlsWorkloadCertRotatedFailed *stats.Int64Measure

	// Actor metrics
	actorStatusReportTotal       *stats.Int64Measure
	actorStatusReportFailedTotal *stats.Int64Measure
	actorTableOperationRecvTotal *stats.Int64Measure
	actorRebalancedTotal         *stats.Int64Measure
	actorActivatedTotal          *stats.Int64Measure
	actorActivatedFailedTotal    *stats.Int64Measure
	actorDeactivationTotal       *stats.Int64Measure
	actorDeactivationFailedTotal *stats.Int64Measure

	// Metrics Tags
	appIDTag      tag.Key
	componentTag  tag.Key
	failReasonTag tag.Key
	operationTag  tag.Key

	// Actor Tags
	actorTypeTag tag.Key
	actorIDTag   tag.Key

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
	appIDTag := tag.MustNewKey("app_id")
	componentTag := tag.MustNewKey("component")
	failReasonTag := tag.MustNewKey("reason")
	operationTag := tag.MustNewKey("operation")
	actorTypeTag := tag.MustNewKey("actor_type")
	actorIDTag := tag.MustNewKey("actor_id")

	return &ServiceMetrics{
		// Runtime Component metrics
		componentLoaded: stats.Int64(
			"runtime/component/loaded",
			"The number of successfully loaded components",
			stats.UnitDimensionless),
		componentInitCompleted: stats.Int64(
			"runtime/component/init_total",
			"The number of initialized components",
			stats.UnitDimensionless),
		componentInitFailed: stats.Int64(
			"runtime/component/init_fail_total",
			"The number of component initialization failures",
			stats.UnitDimensionless),

		// mTLS
		mtlsInitCompleted: stats.Int64(
			"runtime/mtls/init_total",
			"The number of successful mTLS authenticator initialization.",
			stats.UnitDimensionless),
		mtlsInitFailed: stats.Int64(
			"runtime/mtls/init_fail_total",
			"The number of mTLS authenticator init failures",
			stats.UnitDimensionless),
		mtlsWorkloadCertRotated: stats.Int64(
			"runtime/mtls/workload_cert_rotated_total",
			"The number of the successful workload certificate rotations",
			stats.UnitDimensionless),
		mtlsWorkloadCertRotatedFailed: stats.Int64(
			"runtime/mtls/workload_cert_rotated_fail_total",
			"The number of the failed workload certificate rotations",
			stats.UnitDimensionless),

		// Actor
		actorStatusReportTotal: stats.Int64(
			"runtime/actor/status_report_total",
			"The number of the successful status reports to placement service.",
			stats.UnitDimensionless),
		actorStatusReportFailedTotal: stats.Int64(
			"runtime/actor/status_report_fail_total",
			"The number of the failed status reports to placement service",
			stats.UnitDimensionless),
		actorTableOperationRecvTotal: stats.Int64(
			"runtime/actor/table_operation_recv_total",
			"The number of the received actor placement table operations.",
			stats.UnitDimensionless),
		actorRebalancedTotal: stats.Int64(
			"runtime/actor/reblanaced_total",
			"The number of the actor rebalance requests.",
			stats.UnitDimensionless),
		actorActivatedTotal: stats.Int64(
			"runtime/actor/activated_total",
			"The number of the actor activation.",
			stats.UnitDimensionless),
		actorActivatedFailedTotal: stats.Int64(
			"runtime/actor/activated_failed_total",
			"The number of the actor activation failures.",
			stats.UnitDimensionless),
		actorDeactivationTotal: stats.Int64(
			"runtime/actor/deactivated_total",
			"The number of the successful actor deactivation.",
			stats.UnitDimensionless),
		actorDeactivationFailedTotal: stats.Int64(
			"runtime/actor/deactivated_failed_total",
			"The number of the failed actor deactivation.",
			stats.UnitDimensionless),

		appIDTag:      appIDTag,
		componentTag:  componentTag,
		failReasonTag: failReasonTag,
		operationTag:  operationTag,

		actorTypeTag: actorTypeTag,
		actorIDTag:   actorIDTag,

		ctx: context.Background(),
	}
}

// Init initialize metrics views for metrics
func (s *ServiceMetrics) Init(appID string) error {
	s.ctx, _ = tag.New(s.ctx, tag.Insert(s.appIDTag, appID))

	err := view.Register(
		newView(s.componentLoaded, []tag.Key{s.appIDTag}, view.Count()),
		newView(s.componentInitCompleted, []tag.Key{s.appIDTag, s.componentTag}, view.Count()),
		newView(s.componentInitFailed, []tag.Key{s.appIDTag, s.componentTag, s.failReasonTag}, view.Count()),

		newView(s.mtlsInitCompleted, []tag.Key{s.appIDTag}, view.Count()),
		newView(s.mtlsInitFailed, []tag.Key{s.appIDTag, s.failReasonTag}, view.Count()),
		newView(s.mtlsWorkloadCertRotated, []tag.Key{s.appIDTag}, view.Count()),
		newView(s.mtlsWorkloadCertRotatedFailed, []tag.Key{s.appIDTag, s.failReasonTag}, view.Count()),

		newView(s.actorStatusReportTotal, []tag.Key{s.appIDTag, s.actorTypeTag, s.actorIDTag, s.operationTag}, view.Count()),
		newView(s.actorStatusReportFailedTotal, []tag.Key{s.appIDTag, s.actorTypeTag, s.actorIDTag, s.operationTag, s.failReasonTag}, view.Count()),
		newView(s.actorTableOperationRecvTotal, []tag.Key{s.appIDTag, s.actorTypeTag, s.actorIDTag, s.operationTag}, view.Count()),
		newView(s.actorRebalancedTotal, []tag.Key{s.appIDTag, s.actorTypeTag, s.actorIDTag}, view.Count()),
		newView(s.actorActivatedTotal, []tag.Key{s.appIDTag, s.actorTypeTag, s.actorIDTag}, view.Count()),
		newView(s.actorActivatedFailedTotal, []tag.Key{s.appIDTag, s.actorTypeTag, s.actorIDTag}, view.Count()),
		newView(s.actorDeactivationTotal, []tag.Key{s.appIDTag, s.actorTypeTag, s.actorIDTag}, view.Count()),
		newView(s.actorDeactivationFailedTotal, []tag.Key{s.appIDTag, s.actorTypeTag, s.actorIDTag}, view.Count()),
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
		[]tag.Mutator{tag.Upsert(s.componentTag, component), tag.Upsert(s.failReasonTag, reason)},
		s.componentInitFailed.M(1))
}

// MTLSInitCompleted records metric when component is initialized
func (s *ServiceMetrics) MTLSInitCompleted() {
	stats.Record(s.ctx, s.mtlsInitCompleted.M(1))
}

// MTLSInitFailed records metric when component initialization is failed
func (s *ServiceMetrics) MTLSInitFailed(reason string) {
	stats.RecordWithTags(
		s.ctx, []tag.Mutator{tag.Upsert(s.failReasonTag, reason)},
		s.mtlsInitFailed.M(1))
}

// MTLSWorkLoadCertRotationCompleted records metric when workload certificate rotation is succeeded
func (s *ServiceMetrics) MTLSWorkLoadCertRotationCompleted() {
	stats.Record(s.ctx, s.mtlsWorkloadCertRotated.M(1))
}

// MTLSWorkLoadCertRotationFailed records metric when workload certificate rotation is failed
func (s *ServiceMetrics) MTLSWorkLoadCertRotationFailed(reason string) {
	stats.RecordWithTags(
		s.ctx, []tag.Mutator{tag.Upsert(s.failReasonTag, reason)},
		s.mtlsWorkloadCertRotatedFailed.M(1))
}

// ActorStatusReported records metrics when status is reported to placement service.
func (s *ServiceMetrics) ActorStatusReported(operation string) {
	stats.RecordWithTags(
		s.ctx, []tag.Mutator{tag.Upsert(s.operationTag, operation)},
		s.actorStatusReportTotal.M(1))
}

// ActorStatusReportFailed records metrics when status report to placement service is failed.
func (s *ServiceMetrics) ActorStatusReportFailed(operation string, reason string) {
	stats.RecordWithTags(
		s.ctx, []tag.Mutator{tag.Upsert(s.operationTag, operation), tag.Upsert(s.failReasonTag, reason)},
		s.actorStatusReportFailedTotal.M(1))
}

// ActorPlacementTableOperationReceived records metric when runtime receives table operation.
func (s *ServiceMetrics) ActorPlacementTableOperationReceived(operation string) {
	stats.RecordWithTags(
		s.ctx, []tag.Mutator{tag.Upsert(s.operationTag, operation)},
		s.actorTableOperationRecvTotal.M(1))
}

// ActorRebalanced records metric when actors are drained.
func (s *ServiceMetrics) ActorRebalanced(actorType, actorID string) {
	stats.RecordWithTags(
		s.ctx, []tag.Mutator{
			tag.Upsert(s.actorTypeTag, actorType),
			tag.Upsert(s.actorIDTag, actorID),
		},
		s.actorRebalancedTotal.M(1))
}

// ActorActivated records metric when actor is activated.
func (s *ServiceMetrics) ActorActivated(actorType, actorID string) {
	stats.RecordWithTags(
		s.ctx, []tag.Mutator{
			tag.Upsert(s.actorTypeTag, actorType),
			tag.Upsert(s.actorIDTag, actorID),
		},
		s.actorActivatedTotal.M(1))
}

// ActorActivationFailed records metric when actor activation is failed.
func (s *ServiceMetrics) ActorActivationFailed(actorType, actorID string, reason string) {
	stats.RecordWithTags(
		s.ctx, []tag.Mutator{
			tag.Upsert(s.actorTypeTag, actorType),
			tag.Upsert(s.actorIDTag, actorID),
			tag.Upsert(s.failReasonTag, reason),
		},
		s.actorActivatedFailedTotal.M(1))
}

// ActorDeactivated records metric when actor is deactivated.
func (s *ServiceMetrics) ActorDeactivated(actorType, actorID string) {
	stats.RecordWithTags(
		s.ctx, []tag.Mutator{
			tag.Upsert(s.actorTypeTag, actorType),
			tag.Upsert(s.actorIDTag, actorID),
		},
		s.actorDeactivationTotal.M(1))
}

// ActorDeactivationFailed records metric when actor deactivation is failed.
func (s *ServiceMetrics) ActorDeactivationFailed(actorType, actorID, reason string) {
	stats.RecordWithTags(
		s.ctx, []tag.Mutator{
			tag.Upsert(s.actorTypeTag, actorType),
			tag.Upsert(s.actorIDTag, actorID),
			tag.Upsert(s.failReasonTag, reason),
		},
		s.actorDeactivationFailedTotal.M(1))
}
