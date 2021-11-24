// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation and Dapr Contributors.
// Licensed under the MIT License.
// ------------------------------------------------------------

package cache

import (
	"testing"
	"time"

	"github.com/dapr/components-contrib/secretstores"

	"github.com/pkg/errors"
	"github.com/stretchr/testify/assert"
)

const (
	testStoreName = "secretStore"
)

var testGetReq = secretstores.GetSecretRequest{Name: "key"}

func TestSecretStoreCaches(t *testing.T) {
	metadata := map[string]string{"cacheEnable": "true", "cacheTTL": "1m", "cacheMemoryLimit": "10000"}
	err := InitSecretStoreCaches(testStoreName, metadata)
	assert.Nil(t, err)
	enable := EnabledForSecretStore(testStoreName)
	assert.True(t, enable)
}

func TestValue(t *testing.T) {
	testValue1 := map[string]string{"data": "data"}
	testValue2 := map[string]string{"data": "data2"}
	metadata := map[string]string{"cacheEnable": "true", "cacheTTL": "1m", "cacheMemoryLimit": "10000"}
	err := InitSecretStoreCaches(testStoreName, metadata)
	assert.Nil(t, err)

	t.Run("test set sync and get", func(t *testing.T) {
		err := SetValueSync(testStoreName, testGetReq, testValue1)
		assert.Nil(t, err)
		value, err := GetValue(testStoreName, testGetReq)
		assert.Nil(t, err)
		assert.Equal(t, testValue1, value)
	})

	t.Run("test set async and get", func(t *testing.T) {
		testGetReq2 := secretstores.GetSecretRequest{Name: "key2"}
		err := SetValueAsync(testStoreName, testGetReq2, testValue2)
		assert.Nil(t, err)
		found := false
		for i := 0; i < 10; i++ {
			value, err := GetValue(testStoreName, testGetReq2)
			if errors.Is(err, ErrNotFound) {
				time.Sleep(time.Second)
				continue
			}
			found = true
			assert.Equal(t, testValue2, value)
		}
		assert.True(t, found)
	})

	t.Run("test version", func(t *testing.T) {
		testGetReqWithVersion2 := testGetReq
		testGetReqWithVersion2.Metadata = map[string]string{versionID: "2"}

		err := SetValueSync(testStoreName, testGetReq, testValue1)
		assert.Nil(t, err)
		_, err = GetValue(testStoreName, testGetReqWithVersion2)
		assert.Equal(t, err, ErrNotFound)

		err = SetValueSync(testStoreName, testGetReqWithVersion2, testValue2)
		assert.Nil(t, err)
		value, err := GetValue(testStoreName, testGetReqWithVersion2)
		assert.Nil(t, err)
		assert.Equal(t, testValue2, value)
		value, err = GetValue(testStoreName, testGetReq)
		assert.Nil(t, err)
		assert.Equal(t, testValue1, value)
	})
}

func TestTTL(t *testing.T) {
	testValue1 := map[string]string{"data": "data"}
	metadata := map[string]string{"cacheEnable": "true", "cacheTTL": "1s", "cacheMemoryLimit": "10000"}
	err := InitSecretStoreCaches(testStoreName, metadata)
	assert.Nil(t, err)

	err = SetValueSync(testStoreName, testGetReq, testValue1)
	assert.Nil(t, err)
	value, err := GetValue(testStoreName, testGetReq)
	assert.Nil(t, err)
	assert.Equal(t, testValue1, value)

	time.Sleep(10 * time.Second)
	_, err = GetValue(testStoreName, testGetReq)
	assert.Equal(t, err, ErrNotFound)
}

func TestMemoryLimit(t *testing.T) {
	testValue1 := map[string]string{"data": "data1"}
	testValue2 := map[string]string{"data": "data2"}
	testGetReq2 := secretstores.GetSecretRequest{Name: "key2"}

	metadata := map[string]string{"cacheEnable": "true", "cacheTTL": "1m", "cacheMemoryLimit": "100"}
	err := InitSecretStoreCaches(testStoreName, metadata)
	assert.Nil(t, err)

	err = SetValueSync(testStoreName, testGetReq, testValue1)
	assert.Nil(t, err)
	value, err := GetValue(testStoreName, testGetReq)
	assert.Nil(t, err)
	assert.Equal(t, testValue1, value)

	err = SetValueSync(testStoreName, testGetReq2, testValue2)
	assert.Nil(t, err)
	// after set the second item the first item is supposed to be evicted
	_, err = GetValue(testStoreName, testGetReq)
	assert.Equal(t, err, ErrNotFound)
}

func TestEncryptAndDecrypt(t *testing.T) {
	testData := []byte("a test data")

	key, err := randomKey()
	assert.Nil(t, err)
	gcm, err := createCipher(key)
	assert.Nil(t, err)

	encrypted, err := encrypt(testData, gcm)
	assert.Nil(t, err)
	decrypted, err := decrypt(encrypted, gcm)
	assert.Nil(t, err)
	assert.Equal(t, testData, decrypted)
}
