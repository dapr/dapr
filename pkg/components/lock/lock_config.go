package lock

import (
	"fmt"
	"strings"
	"sync"
)

const (
	strategyKey = "keyprefix"

	strategyAppid     = "appid"
	strategyStoreName = "name"
	strategyNone      = "none"
	strategyDefault   = strategyAppid

	apiPrefix = "lock"
	separator = "||"
)

var (
	locksConfigurationMu sync.RWMutex
	lockConfiguration    = map[string]*StoreConfiguration{}
)

type StoreConfiguration struct {
	keyPrefixStrategy string
}

func SaveLockConfiguration(storeName string, metadata map[string]string) error {
	strategy := strategyDefault
	for k, v := range metadata {
		if strings.ToLower(k) == strategyKey { //nolint:gocritic
			strategy = strings.ToLower(v)
			break
		}
	}

	err := checkKeyIllegal(strategy)
	if err != nil {
		return err
	}

	locksConfigurationMu.Lock()
	lockConfiguration[storeName] = &StoreConfiguration{keyPrefixStrategy: strategy}
	locksConfigurationMu.Unlock()
	return nil
}

func GetModifiedLockKey(key, storeName, appID string) (string, error) {
	if err := checkKeyIllegal(key); err != nil {
		return "", err
	}
	config := getConfiguration(storeName)
	switch config.keyPrefixStrategy {
	case strategyNone:
		// `lock||key`
		return apiPrefix + separator + key, nil
	case strategyStoreName:
		// `lock||store_name||key`
		return apiPrefix + separator + storeName + separator + key, nil
	case strategyAppid:
		// `lock||key` or `lock||app_id||key`
		if appID == "" {
			return apiPrefix + separator + key, nil
		}
		return apiPrefix + separator + appID + separator + key, nil
	default:
		// `lock||keyPrefixStrategy||key`
		return apiPrefix + separator + config.keyPrefixStrategy + separator + key, nil
	}
}

func getConfiguration(storeName string) *StoreConfiguration {
	locksConfigurationMu.RLock()
	c := lockConfiguration[storeName]
	if c != nil {
		locksConfigurationMu.RUnlock()
		return c
	}
	locksConfigurationMu.RUnlock()

	// Acquire a write lock now to update the value in cache
	locksConfigurationMu.Lock()
	defer locksConfigurationMu.Unlock()

	// Try checking the cache again after acquiring a write lock, in case another goroutine has created the object
	c = lockConfiguration[storeName]
	if c != nil {
		return c
	}

	c = &StoreConfiguration{keyPrefixStrategy: strategyDefault}
	lockConfiguration[storeName] = c

	return c
}

func checkKeyIllegal(key string) error {
	if strings.Contains(key, separator) {
		return fmt.Errorf("input key/keyPrefix '%s' can't contain '%s'", key, separator)
	}
	return nil
}
