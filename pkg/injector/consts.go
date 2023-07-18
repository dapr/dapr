package injector

const (
	SidecarContainerName           = "daprd" // Name of the Dapr sidecar container
	SidecarHTTPPortName            = "dapr-http"
	SidecarGRPCPortName            = "dapr-grpc"
	SidecarInternalGRPCPortName    = "dapr-internal"
	SidecarMetricsPortName         = "dapr-metrics"
	SidecarDebugPortName           = "dapr-debug"
	SidecarHealthzPath             = "healthz"
	SidecarInjectedLabel           = "dapr.io/sidecar-injected"
	SidecarAppIDLabel              = "dapr.io/app-id"
	SidecarMetricsEnabledLabel     = "dapr.io/metrics-enabled"
	APIVersionV1                   = "v1.0"
	UnixDomainSocketVolume         = "dapr-unix-domain-socket" // Name of the UNIX domain socket volume.
	UserContainerAppProtocolName   = "APP_PROTOCOL"            // Name of the variable exposed to the app containing the app protocol.
	UserContainerDaprHTTPPortName  = "DAPR_HTTP_PORT"          // Name of the variable exposed to the app containing the Dapr HTTP port.
	UserContainerDaprGRPCPortName  = "DAPR_GRPC_PORT"          // Name of the variable exposed to the app containing the Dapr gRPC port.
	PatchPathLabels                = "/metadata/labels"
	TokenVolumeKubernetesMountPath = "/var/run/secrets/dapr.io/sentrytoken" /* #nosec */ // Mount path for the Kubernetes service account volume with the sentry token.
	TokenVolumeName                = "dapr-identity-token"                  /* #nosec */ // Name of the volume with the service account token for daprd.
)
