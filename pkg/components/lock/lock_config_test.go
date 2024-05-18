package lock

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

const key = "lock-key-1234567"

func TestMain(m *testing.M) {
	SaveLockConfiguration("store1", map[string]string{strategyKey: strategyNone})
	SaveLockConfiguration("store2", map[string]string{strategyKey: strategyAppid})
	SaveLockConfiguration("store3", map[string]string{strategyKey: strategyDefault})
	SaveLockConfiguration("store4", map[string]string{strings.ToUpper(strategyKey): strategyStoreName})
	SaveLockConfiguration("store5", map[string]string{strategyKey: "other-fixed-prefix"})
	// if strategyKey not set
	SaveLockConfiguration("store6", map[string]string{})
	os.Exit(m.Run())
}

func TestSaveLockConfiguration(t *testing.T) {
	testIllegalKeys := []struct {
		storename string
		prefix    string
	}{
		{
			storename: "lockstore01",
			prefix:    "a||b",
		},
	}
	for _, item := range testIllegalKeys {
		err := SaveLockConfiguration(item.storename, map[string]string{
			strategyKey: item.prefix,
		})
		require.Error(t, err)
	}
}

func TestGetModifiedLockKey(t *testing.T) {
	// use custom prefix key
	testIllegalKeys := []struct {
		storename string
		prefix    string
		key       string
	}{
		{
			storename: "lockstore01",
			prefix:    "a",
			key:       "c||d",
		},
	}
	for _, item := range testIllegalKeys {
		err := SaveLockConfiguration(item.storename, map[string]string{
			strategyKey: item.prefix,
		})
		require.NoError(t, err)
		_, err = GetModifiedLockKey(item.key, item.storename, "")
		require.Error(t, err)
	}
}

func TestNonePrefix(t *testing.T) {
	modifiedLockKey, _ := GetModifiedLockKey(key, "store1", "appid1")
	require.Equal(t, "lock||"+key, modifiedLockKey)
}

func TestAppidPrefix(t *testing.T) {
	modifiedLockKey, _ := GetModifiedLockKey(key, "store2", "appid1")
	require.Equal(t, "lock||appid1||lock-key-1234567", modifiedLockKey)
}

func TestAppidPrefix_WithEnptyAppid(t *testing.T) {
	modifiedLockKey, _ := GetModifiedLockKey(key, "store2", "")
	require.Equal(t, "lock||lock-key-1234567", modifiedLockKey)
}

func TestDefaultPrefix(t *testing.T) {
	modifiedLockKey, _ := GetModifiedLockKey(key, "store3", "appid1")
	require.Equal(t, "lock||appid1||lock-key-1234567", modifiedLockKey)
}

func TestStoreNamePrefix(t *testing.T) {
	key := "lock-key-1234567"

	modifiedLockKey, _ := GetModifiedLockKey(key, "store4", "appid1")
	require.Equal(t, "lock||store4||lock-key-1234567", modifiedLockKey)
}

func TestOtherFixedPrefix(t *testing.T) {
	modifiedLockKey, _ := GetModifiedLockKey(key, "store5", "appid1")
	require.Equal(t, "lock||other-fixed-prefix||lock-key-1234567", modifiedLockKey)
}

func TestLegacyPrefix(t *testing.T) {
	modifiedLockKey, _ := GetModifiedLockKey(key, "store6", "appid1")
	require.Equal(t, "lock||appid1||lock-key-1234567", modifiedLockKey)
}

func TestPrefix_StoreNotInitial(t *testing.T) {
	// no config for store999
	modifiedLockKey, _ := GetModifiedLockKey(key, "store999", "appid99")
	require.Equal(t, "lock||appid99||lock-key-1234567", modifiedLockKey)
}
