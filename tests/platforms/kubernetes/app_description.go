// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package kubernetes

// AppDescription holds the deployment information of test app
type AppDescription struct {
	AppName        string
	AppPort        int
	AppProtocol    string
	DaprEnabled    bool
	ImageName      string
	RegistryName   string
	Replicas       int32
	IngressEnabled bool
	MetricsPort    string
}
