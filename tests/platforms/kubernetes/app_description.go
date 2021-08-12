// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation and Dapr Contributors.
// Licensed under the MIT License.
// ------------------------------------------------------------

package kubernetes

// AppDescription holds the deployment information of test app.
type AppDescription struct {
	AppName           string
	AppPort           int
	AppProtocol       string
	AppEnv            map[string]string
	DaprEnabled       bool
	ImageName         string
	ImageSecret       string
	RegistryName      string
	Replicas          int32
	IngressEnabled    bool
	MetricsEnabled    bool // This controls the setting for the dapr.io/enable-metrics annotation
	MetricsPort       string
	Config            string
	AppCPULimit       string
	AppCPURequest     string
	AppMemoryLimit    string
	AppMemoryRequest  string
	DaprCPULimit      string
	DaprCPURequest    string
	DaprMemoryLimit   string
	DaprMemoryRequest string
	Namespace         *string
	IsJob             bool
}
