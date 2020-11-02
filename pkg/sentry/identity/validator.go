package identity

// Validator is used to validate the identity of a certificate requester by using an ID and token.
type Validator interface {
	Validate(id, token, namespace string) error
}
