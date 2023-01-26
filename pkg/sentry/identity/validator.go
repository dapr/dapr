package identity

import "context"

// Validator is used to validate the identity of a certificate requester by using an ID and token.
type Validator interface {
	Validate(ctx context.Context, id, token, namespace string) error
}
