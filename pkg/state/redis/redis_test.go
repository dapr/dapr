package redis

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestGetKeyVersion(t *testing.T) {
	store := NewRedisStateStore()
	t.Run("With all required fields", func(t *testing.T) {
		key, ver, err := store.getKeyVersion([]interface{}{"data", "TEST_KEY", "version", "TEST_VER"})
		assert.Equal(t, nil, err, "failed to read all fields")
		assert.Equal(t, "TEST_KEY", key, "failed to read key")
		assert.Equal(t, "TEST_VER", ver, "failed to read version")
	})
	t.Run("With missing data", func(t *testing.T) {
		_, _, err := store.getKeyVersion([]interface{}{"version", "TEST_VER"})
		assert.NotNil(t, err, "failed to respond to missing data field")
	})
	t.Run("With missing version", func(t *testing.T) {
		_, _, err := store.getKeyVersion([]interface{}{"data", "TEST_KEY"})
		assert.NotNil(t, err, "failed to respond to missing version field")
	})
	t.Run("With all required fields - out of order", func(t *testing.T) {
		key, ver, err := store.getKeyVersion([]interface{}{"version", "TEST_VER", "dragon", "TEST_DRAGON", "data", "TEST_KEY"})
		assert.Equal(t, nil, err, "failed to read all fields")
		assert.Equal(t, "TEST_KEY", key, "failed to read key")
		assert.Equal(t, "TEST_VER", ver, "failed to read version")
	})
	t.Run("With no fields", func(t *testing.T) {
		_, _, err := store.getKeyVersion([]interface{}{})
		assert.NotNil(t, err, "failed to respond to missing fields")
	})
	t.Run("With wrong fields", func(t *testing.T) {
		_, _, err := store.getKeyVersion([]interface{}{"dragon", "TEST_DRAGON"})
		assert.NotNil(t, err, "failed to respond to missing fields")
	})
}
