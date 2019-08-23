package redis

import (
	"testing"

	jsoniter "github.com/json-iterator/go"
	"github.com/stretchr/testify/assert"
)

func TestStringify(t *testing.T) {
	testRedisStore := NewRedisStateStore()

	t.Run("string type", func(t *testing.T) {
		var data string
		data = "TestString"
		actual, err := testRedisStore.stringify(data)
		assert.NoError(t, err)
		assert.Equal(t, []byte(data), actual)
	})

	t.Run("byte array type", func(t *testing.T) {
		var data []byte
		data = []byte("TestString")
		actual, err := testRedisStore.stringify(data)
		assert.NoError(t, err)
		assert.Equal(t, data, actual)
	})

	t.Run("map[string]string type", func(t *testing.T) {
		data := map[string]string{
			"key1": "value1",
			"key2": "value2",
		}
		result, err := testRedisStore.stringify(data)
		assert.NoError(t, err)

		var actual map[string]string
		jsoniter.Unmarshal(result, &actual)

		assert.EqualValues(t, data, actual)
	})
}
