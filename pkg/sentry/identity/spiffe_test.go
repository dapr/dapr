package identity

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSPIFFEID(t *testing.T) {
	t.Run("valid arguments", func(t *testing.T) {
		id, err := CreateSPIFFEID("td1", "ns1", "app1")
		assert.NoError(t, err)
		assert.Equal(t, "spiffe://td1/ns/ns1/app1", id)
	})

	t.Run("missing trust domain", func(t *testing.T) {
		id, err := CreateSPIFFEID("", "ns1", "app1")
		assert.Error(t, err)
		assert.Empty(t, id)
	})

	t.Run("missing namespace", func(t *testing.T) {
		id, err := CreateSPIFFEID("td1", "", "app1")
		assert.Error(t, err)
		assert.Empty(t, id)
	})

	t.Run("missing app id", func(t *testing.T) {
		id, err := CreateSPIFFEID("td1", "ns1", "")
		assert.Error(t, err)
		assert.Empty(t, id)
	})

	t.Run("invalid trust domain", func(t *testing.T) {
		id, err := CreateSPIFFEID("td1:a", "ns1", "app1")
		assert.Error(t, err)
		assert.Empty(t, id)
	})

	t.Run("invalid trust domain size", func(t *testing.T) {
		td := ""
		for i := 0; i < 255; i++ {
			td = fmt.Sprintf("%s%v", td, i)
		}

		id, err := CreateSPIFFEID(td, "ns1", "app1")
		assert.Error(t, err)
		assert.Empty(t, id)
	})

	t.Run("invalid spiffe id size", func(t *testing.T) {
		appID := ""
		for i := 0; i < 2048; i++ {
			appID = fmt.Sprintf("%s%v", appID, i)
		}

		id, err := CreateSPIFFEID("td1", "ns1", appID)
		assert.Error(t, err)
		assert.Empty(t, id)
	})
}
