package identity

// Bundle contains all the elements to identify a workload across a trust domain and a namespace.
type Bundle struct {
	ID          string
	Namespace   string
	TrustDomain string
}

// NewBundle returns a new identity bundle.
// namespace and trustDomain are optional parameters. When empty, a nil value is returned.
func NewBundle(id, namespace, trustDomain string) *Bundle {
	// Empty namespace and trust domain result in an empty bundle
	if namespace == "" || trustDomain == "" {
		return nil
	}

	return &Bundle{
		ID:          id,
		Namespace:   namespace,
		TrustDomain: trustDomain,
	}
}
