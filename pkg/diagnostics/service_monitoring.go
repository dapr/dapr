package diagnostics

import (
	"context"

	diag_utils "github.com/dapr/dapr/pkg/diagnostics/utils"
	"go.opencensus.io/stats"
	"go.opencensus.io/stats/view"
	"go.opencensus.io/tag"
)

// Tag keys
var (
	componentKey  = tag.MustNewKey("component")
	failReasonKey = tag.MustNewKey("reason")
	operationKey  = tag.MustNewKey("operation")
	actorTypeKey  = tag.MustNewKey("actor_type")
	actorIDKey    = tag.MustNewKey("actor_id")
)

// serviceMetrics holds dapr runtime metric monitoring methods
type serviceMetrics struct {
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
	actorDeactivationTotal       *stats.Int64Measure
	actorDeactivationFailedTotal *stats.Int64Measure
	actorPendingCalls            *stats.Int64Measure

	appID   string
	ctx     context.Context
	enabled bool
}

// newServiceMetrics returns serviceMetrics instance with default service metric stats
func newServiceMetrics() *serviceMetrics {
	return &serviceMetrics{
		// Runtime Component metrics
		componentLoaded: stats.Int64(
			"runtime/component/loaded",
			"The number of successfully loaded components.",
			stats.UnitDimensionless),
		componentInitCompleted: stats.Int64(
			"runtime/component/init_total",
			"The number of initialized components.",
			stats.UnitDimensionless),
		componentInitFailed: stats.Int64(
			"runtime/component/init_fail_total",
			"The number of component initialization failures.",
			stats.UnitDimensionless),

		// mTLS
		mtlsInitCompleted: stats.Int64(
			"runtime/mtls/init_total",
			"The number of successful mTLS authenticator initialization.",
			stats.UnitDimensionless),
		mtlsInitFailed: stats.Int64(
			"runtime/mtls/init_fail_total",
			"The number of mTLS authenticator init failures.",
			stats.UnitDimensionless),
		mtlsWorkloadCertRotated: stats.Int64(
			"runtime/mtls/workload_cert_rotated_total",
			"The number of the successful workload certificate rotations.",
			stats.UnitDimensionless),
		mtlsWorkloadCertRotatedFailed: stats.Int64(
			"runtime/mtls/workload_cert_rotated_fail_total",
			"The number of the failed workload certificate rotations.",
			stats.UnitDimensionless),

		// Actor
		actorStatusReportTotal: stats.Int64(
			"runtime/actor/status_report_total",
			"The number of the successful status reports to placement service.",
			stats.UnitDimensionless),
		actorStatusReportFailedTotal: stats.Int64(
			"runtime/actor/status_report_fail_total",
			"The number of the failed status reports to placement service.",
			stats.UnitDimensionless),
		actorTableOperationRecvTotal: stats.Int64(
			"runtime/actor/table_operation_recv_total",
			"The number of the received actor placement table operations.",
			stats.UnitDimensionless),
		actorRebalancedTotal: stats.Int64(
			"runtime/actor/rebalanced_total",
			"The number of the actor rebalance requests.",
			stats.UnitDimensionless),
		actorDeactivationTotal: stats.Int64(
			"runtime/actor/deactivated_total",
			"The number of the successful actor deactivation.",
			stats.UnitDimensionless),
		actorDeactivationFailedTotal: stats.Int64(
			"runtime/actor/deactivated_failed_total",
			"The number of the failed actor deactivation.",
			stats.UnitDimensionless),
		actorPendingCalls: stats.Int64(
			"runtime/actor/pending_actor_calls",
			"The number of pending actor calls waiting to acquire the per-actor lock.",
			stats.UnitDimensionless),

		// TODO: use the correct context for each request
		ctx:     context.Background(),
		enabled: false,
	}
}

// Init initialize metrics views for metrics
func (s *serviceMetrics) Init(appID string) error {
	s.appID = appID
	s.enabled = true
	return view.Register(
		diag_utils.NewMeasureView(s.componentLoaded, []tag.Key{appIDKey}, view.Count()),
		diag_utils.NewMeasureView(s.componentInitCompleted, []tag.Key{appIDKey, componentKey}, view.Count()),
		diag_utils.NewMeasureView(s.componentInitFailed, []tag.Key{appIDKey, componentKey, failReasonKey}, view.Count()),

		diag_utils.NewMeasureView(s.mtlsInitCompleted, []tag.Key{appIDKey}, view.Count()),
		diag_utils.NewMeasureView(s.mtlsInitFailed, []tag.Key{appIDKey, failReasonKey}, view.Count()),
		diag_utils.NewMeasureView(s.mtlsWorkloadCertRotated, []tag.Key{appIDKey}, view.Count()),
		diag_utils.NewMeasureView(s.mtlsWorkloadCertRotatedFailed, []tag.Key{appIDKey, failReasonKey}, view.Count()),

		diag_utils.NewMeasureView(s.actorStatusReportTotal, []tag.Key{appIDKey, actorTypeKey, operationKey}, view.Count()),
		diag_utils.NewMeasureView(s.actorStatusReportFailedTotal, []tag.Key{appIDKey, actorTypeKey, operationKey, failReasonKey}, view.Count()),
		diag_utils.NewMeasureView(s.actorTableOperationRecvTotal, []tag.Key{appIDKey, actorTypeKey, operationKey}, view.Count()),
		diag_utils.NewMeasureView(s.actorRebalancedTotal, []tag.Key{appIDKey, actorTypeKey}, view.Count()),
		diag_utils.NewMeasureView(s.actorDeactivationTotal, []tag.Key{appIDKey, actorTypeKey}, view.Count()),
		diag_utils.NewMeasureView(s.actorDeactivationFailedTotal, []tag.Key{appIDKey, actorTypeKey}, view.Count()),
		diag_utils.NewMeasureView(s.actorPendingCalls, []tag.Key{appIDKey, actorTypeKey, actorIDKey}, view.LastValue()),
	)
}

// ComponentLoaded records metric when component is loaded successfully
func (s *serviceMetrics) ComponentLoaded() {
	if s.enabled {
		stats.RecordWithTags(s.ctx, diag_utils.WithTags(appIDKey, s.appID), s.componentLoaded.M(1))
	}
}

// ComponentInitialized records metric when component is initialized
func (s *serviceMetrics) ComponentInitialized(component string) {
	if s.enabled {
		stats.RecordWithTags(
			s.ctx,
			diag_utils.WithTags(appIDKey, s.appID, componentKey, component),
			s.componentInitCompleted.M(1))
	}
}

// ComponentInitFailed records metric when component initialization is failed
func (s *serviceMetrics) ComponentInitFailed(component string, reason string) {
	if s.enabled {
		stats.RecordWithTags(
			s.ctx,
			diag_utils.WithTags(appIDKey, s.appID, componentKey, component, failReasonKey, reason),
			s.componentInitFailed.M(1))
	}
}

// MTLSInitCompleted records metric when component is initialized
func (s *serviceMetrics) MTLSInitCompleted() {
	if s.enabled {
		stats.RecordWithTags(s.ctx, diag_utils.WithTags(appIDKey, s.appID), s.mtlsInitCompleted.M(1))
	}
}

// MTLSInitFailed records metric when component initialization is failed
func (s *serviceMetrics) MTLSInitFailed(reason string) {
	if s.enabled {
		stats.RecordWithTags(
			s.ctx, diag_utils.WithTags(appIDKey, s.appID, failReasonKey, reason),
			s.mtlsInitFailed.M(1))
	}
}

// MTLSWorkLoadCertRotationCompleted records metric when workload certificate rotation is succeeded
func (s *serviceMetrics) MTLSWorkLoadCertRotationCompleted() {
	if s.enabled {
		stats.RecordWithTags(s.ctx, diag_utils.WithTags(appIDKey, s.appID), s.mtlsWorkloadCertRotated.M(1))
	}
}

// MTLSWorkLoadCertRotationFailed records metric when workload certificate rotation is failed
func (s *serviceMetrics) MTLSWorkLoadCertRotationFailed(reason string) {
	if s.enabled {
		stats.RecordWithTags(
			s.ctx, diag_utils.WithTags(appIDKey, s.appID, failReasonKey, reason),
			s.mtlsWorkloadCertRotatedFailed.M(1))
	}
}

// ActorStatusReported records metrics when status is reported to placement service.
func (s *serviceMetrics) ActorStatusReported(operation string) {
	if s.enabled {
		stats.RecordWithTags(
			s.ctx, diag_utils.WithTags(appIDKey, s.appID, operationKey, operation),
			s.actorStatusReportTotal.M(1))
	}
}

// ActorStatusReportFailed records metrics when status report to placement service is failed.
func (s *serviceMetrics) ActorStatusReportFailed(operation string, reason string) {
	if s.enabled {
		stats.RecordWithTags(
			s.ctx, diag_utils.WithTags(appIDKey, s.appID, operationKey, operation, failReasonKey, reason),
			s.actorStatusReportFailedTotal.M(1))
	}
}

// ActorPlacementTableOperationReceived records metric when runtime receives table operation.
func (s *serviceMetrics) ActorPlacementTableOperationReceived(operation string) {
	if s.enabled {
		stats.RecordWithTags(
			s.ctx, diag_utils.WithTags(appIDKey, s.appID, operationKey, operation),
			s.actorTableOperationRecvTotal.M(1))
	}
}

// ActorRebalanced records metric when actors are drained.
func (s *serviceMetrics) ActorRebalanced(actorType string) {
	if s.enabled {
		stats.RecordWithTags(
			s.ctx,
			diag_utils.WithTags(appIDKey, s.appID, actorTypeKey, actorType),
			s.actorRebalancedTotal.M(1))
	}
}

// ActorDeactivated records metric when actor is deactivated.
func (s *serviceMetrics) ActorDeactivated(actorType string) {
	if s.enabled {
		stats.RecordWithTags(
			s.ctx,
			diag_utils.WithTags(appIDKey, s.appID, actorTypeKey, actorType),
			s.actorDeactivationTotal.M(1))
	}
}

// ActorDeactivationFailed records metric when actor deactivation is failed.
func (s *serviceMetrics) ActorDeactivationFailed(actorType, reason string) {
	if s.enabled {
		stats.RecordWithTags(
			s.ctx,
			diag_utils.WithTags(appIDKey, s.appID, actorTypeKey, actorType, failReasonKey, reason),
			s.actorDeactivationFailedTotal.M(1))
	}
}

// ReportCurrentPendingLocks records the current pending actor locks.
func (s *serviceMetrics) ReportCurrentPendingLocks(actorType, actorID string, pendingLocks int32) {
	if s.enabled {
		stats.RecordWithTags(
			s.ctx,
			diag_utils.WithTags(appIDKey, s.appID, actorTypeKey, actorType, actorIDKey, actorID),
			s.actorPendingCalls.M(int64(pendingLocks)))
	}
}
