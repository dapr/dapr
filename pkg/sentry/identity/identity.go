package identity

// Bundle contains all the elements to identify a workload across a trust domain and a namespace
type Bundle struct {
	ID          string
	Namespace   string
	TrustDomain string
}

// NewBundle returns a new identity bundle
func NewBundle(id, namespace, trustDomain string) *Bundle {
	return &Bundle{
		ID:          id,
		Namespace:   namespace,
		TrustDomain: trustDomain,
	}
}
