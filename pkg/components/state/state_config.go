/*
Copyright 2021 The Dapr Authors
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at
    http://www.apache.org/licenses/LICENSE-2.0
Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package state

import (
	"fmt"
	"os"
	"strings"
	"sync"

	"github.com/dapr/dapr/pkg/api/errors"
)

const (
	strategyKey = "keyprefix"

	strategyNamespace = "namespace"
	strategyAppid     = "appid"
	strategyStoreName = "name"
	strategyNone      = "none"
	strategyDefault   = strategyAppid

	daprSeparator = "||"
)

var (
	statesConfigurationLock sync.RWMutex
	statesConfiguration     = map[string]*StoreConfiguration{}
	namespace               = os.Getenv("NAMESPACE")
)

type StoreConfiguration struct {
	keyPrefixStrategy string
}

func SaveStateConfiguration(storeName string, metadata map[string]string) error {
	strategy := strategyDefault
	for k, v := range metadata {
		if strings.ToLower(k) == strategyKey { //nolint:gocritic
			strategy = strings.ToLower(v)
			break
		}
	}

	err := checkKeyIllegal(strategy)
	if err != nil {
		return errors.StateStoreInvalidKeyName(storeName, strategy, err.Error())
	}

	statesConfigurationLock.Lock()
	statesConfiguration[storeName] = &StoreConfiguration{keyPrefixStrategy: strategy}
	statesConfigurationLock.Unlock()
	return nil
}

func GetModifiedStateKey(key, storeName, appID string) (string, error) {
	if err := checkKeyIllegal(key); err != nil {
		return "", errors.StateStoreInvalidKeyName(storeName, key, err.Error())
	}

	stateConfiguration := getStateConfiguration(storeName)
	switch stateConfiguration.keyPrefixStrategy {
	case strategyNone:
		return key, nil
	case strategyStoreName:
		return storeName + daprSeparator + key, nil
	case strategyAppid:
		if appID == "" {
			return key, nil
		}
		return appID + daprSeparator + key, nil
	case strategyNamespace:
		if appID == "" {
			return key, nil
		}
		if namespace == "" {
			// if namespace is empty, fallback to app id strategy
			return appID + daprSeparator + key, nil
		}
		return namespace + "." + appID + daprSeparator + key, nil
	default:
		return stateConfiguration.keyPrefixStrategy + daprSeparator + key, nil
	}
}

func GetOriginalStateKey(modifiedStateKey string) string {
	splits := strings.SplitN(modifiedStateKey, daprSeparator, 3)
	if len(splits) <= 1 {
		return modifiedStateKey
	}
	return splits[1]
}

func getStateConfiguration(storeName string) *StoreConfiguration {
	statesConfigurationLock.RLock()
	c := statesConfiguration[storeName]
	if c != nil {
		statesConfigurationLock.RUnlock()
		return c
	}
	statesConfigurationLock.RUnlock()

	// Acquire a write lock now to update the value in cache
	statesConfigurationLock.Lock()
	defer statesConfigurationLock.Unlock()

	// Try checking the cache again after acquiring a write lock, in case another goroutine has created the object
	c = statesConfiguration[storeName]
	if c != nil {
		return c
	}

	c = &StoreConfiguration{keyPrefixStrategy: strategyDefault}
	statesConfiguration[storeName] = c

	return c
}

func checkKeyIllegal(key string) error {
	if strings.Contains(key, daprSeparator) {
		return fmt.Errorf("input key/keyPrefix '%s' can't contain '%s'", key, daprSeparator)
	}
	return nil
}
