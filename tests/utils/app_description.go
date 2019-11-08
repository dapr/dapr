package utils

// AppDescription holds the deployment information of test app
type AppDescription struct {
	AppName        string
	DaprEnabled    bool
	ImageName      string
	RegistryName   string
	Replicas       int32
	IngressEnabled bool
}
