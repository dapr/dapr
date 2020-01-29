package csr

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestGenerateCSRTemplate(t *testing.T) {
	t.Run("valid csr template", func(t *testing.T) {
		tmpl, err := genCSRTemplate("test-org")
		assert.Nil(t, err)
		assert.Equal(t, "test-org", tmpl.Subject.Organization[0])
	})
}
