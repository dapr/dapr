package selfhosted

import "github.com/dapr/dapr/pkg/sentry/identity"

func NewValidator() identity.Validator {
	return &validator{}
}

type validator struct {
}

// no validation for self hosted
func (v *validator) Validate(id, token string) error {
	return nil
}
