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
	actorActivatedTotal          *stats.Int64Measure
	actorActivatedFailedTotal    *stats.Int64Measure
	actorDeactivationTotal       *stats.Int64Measure
	actorDeactivationFailedTotal *stats.Int64Measure

	appID string
	ctx   context.Context
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

		// TODO: use the correct context for each request
		ctx: context.Background(),
	}
}

// Init initialize metrics views for metrics
func (s *serviceMetrics) Init(appID string) error {
	s.appID = appID
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
		diag_utils.NewMeasureView(s.actorActivatedTotal, []tag.Key{appIDKey, actorTypeKey}, view.Count()),
		diag_utils.NewMeasureView(s.actorActivatedFailedTotal, []tag.Key{appIDKey, actorTypeKey}, view.Count()),
		diag_utils.NewMeasureView(s.actorDeactivationTotal, []tag.Key{appIDKey, actorTypeKey}, view.Count()),
		diag_utils.NewMeasureView(s.actorDeactivationFailedTotal, []tag.Key{appIDKey, actorTypeKey}, view.Count()),
	)
}

// ComponentLoaded records metric when component is loaded successfully
func (s *serviceMetrics) ComponentLoaded() {
	stats.RecordWithTags(s.ctx, diag_utils.WithTags(appIDKey, s.appID), s.componentLoaded.M(1))
}

// ComponentInitialized records metric when component is initialized
func (s *serviceMetrics) ComponentInitialized(component string) {
	stats.RecordWithTags(
		s.ctx,
		diag_utils.WithTags(appIDKey, s.appID, componentKey, component),
		s.componentInitCompleted.M(1))
}

// ComponentInitFailed records metric when component initialization is failed
func (s *serviceMetrics) ComponentInitFailed(component string, reason string) {
	stats.RecordWithTags(
		s.ctx,
		diag_utils.WithTags(appIDKey, s.appID, componentKey, component, failReasonKey, reason),
		s.componentInitFailed.M(1))
}

// MTLSInitCompleted records metric when component is initialized
func (s *serviceMetrics) MTLSInitCompleted() {
	stats.RecordWithTags(s.ctx, diag_utils.WithTags(appIDKey, s.appID), s.mtlsInitCompleted.M(1))
}

// MTLSInitFailed records metric when component initialization is failed
func (s *serviceMetrics) MTLSInitFailed(reason string) {
	stats.RecordWithTags(
		s.ctx, diag_utils.WithTags(appIDKey, s.appID, failReasonKey, reason),
		s.mtlsInitFailed.M(1))
}

// MTLSWorkLoadCertRotationCompleted records metric when workload certificate rotation is succeeded
func (s *serviceMetrics) MTLSWorkLoadCertRotationCompleted() {
	stats.RecordWithTags(s.ctx, diag_utils.WithTags(appIDKey, s.appID), s.mtlsWorkloadCertRotated.M(1))
}

// MTLSWorkLoadCertRotationFailed records metric when workload certificate rotation is failed
func (s *serviceMetrics) MTLSWorkLoadCertRotationFailed(reason string) {
	stats.RecordWithTags(
		s.ctx, diag_utils.WithTags(appIDKey, s.appID, failReasonKey, reason),
		s.mtlsWorkloadCertRotatedFailed.M(1))
}

// ActorStatusReported records metrics when status is reported to placement service.
func (s *serviceMetrics) ActorStatusReported(operation string) {
	stats.RecordWithTags(
		s.ctx, diag_utils.WithTags(appIDKey, s.appID, operationKey, operation),
		s.actorStatusReportTotal.M(1))
}

// ActorStatusReportFailed records metrics when status report to placement service is failed.
func (s *serviceMetrics) ActorStatusReportFailed(operation string, reason string) {
	stats.RecordWithTags(
		s.ctx, diag_utils.WithTags(appIDKey, s.appID, operationKey, operation, failReasonKey, reason),
		s.actorStatusReportFailedTotal.M(1))
}

// ActorPlacementTableOperationReceived records metric when runtime receives table operation.
func (s *serviceMetrics) ActorPlacementTableOperationReceived(operation string) {
	stats.RecordWithTags(
		s.ctx, diag_utils.WithTags(appIDKey, s.appID, operationKey, operation),
		s.actorTableOperationRecvTotal.M(1))
}

// ActorRebalanced records metric when actors are drained.
func (s *serviceMetrics) ActorRebalanced(actorType string) {
	stats.RecordWithTags(
		s.ctx,
		diag_utils.WithTags(appIDKey, s.appID, actorTypeKey, actorType),
		s.actorRebalancedTotal.M(1))
}

// ActorActivated records metric when actor is activated.
func (s *serviceMetrics) ActorActivated(actorType string) {
	stats.RecordWithTags(
		s.ctx,
		diag_utils.WithTags(appIDKey, s.appID, actorTypeKey, actorType),
		s.actorActivatedTotal.M(1))
}

// ActorActivationFailed records metric when actor activation is failed.
func (s *serviceMetrics) ActorActivationFailed(actorType string, reason string) {
	stats.RecordWithTags(
		s.ctx,
		diag_utils.WithTags(appIDKey, s.appID, actorTypeKey, actorType, failReasonKey, reason),
		s.actorActivatedFailedTotal.M(1))
}

// ActorDeactivated records metric when actor is deactivated.
func (s *serviceMetrics) ActorDeactivated(actorType string) {
	stats.RecordWithTags(
		s.ctx,
		diag_utils.WithTags(appIDKey, s.appID, actorTypeKey, actorType),
		s.actorDeactivationTotal.M(1))
}

// ActorDeactivationFailed records metric when actor deactivation is failed.
func (s *serviceMetrics) ActorDeactivationFailed(actorType, reason string) {
	stats.RecordWithTags(
		s.ctx,
		diag_utils.WithTags(appIDKey, s.appID, actorTypeKey, actorType, failReasonKey, reason),
		s.actorDeactivationFailedTotal.M(1))
}
