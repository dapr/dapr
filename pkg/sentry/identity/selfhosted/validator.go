package selfhosted

import (
	"context"

	"github.com/dapr/dapr/pkg/sentry/identity"
)

func NewValidator() identity.Validator {
	return &validator{}
}

type validator struct{}

func (v *validator) Validate(_ context.Context, id, token, namespace string) error {
	// no validation for self hosted.
	return nil
}
