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

	"github.com/pkg/errors"
)

const (
	strategyKey = "keyPrefix"

	strategyNamespace = "namespace"
	strategyAppid     = "appid"
	strategyStoreName = "name"
	strategyNone      = "none"
	strategyDefault   = strategyAppid

	daprSeparator = "||"
)

var (
	statesConfigurationLock sync.Mutex
	statesConfiguration     = map[string]*StoreConfiguration{}
	namespace               = os.Getenv("NAMESPACE")
)

type StoreConfiguration struct {
	keyPrefixStrategy string
}

func SaveStateConfiguration(storeName string, metadata map[string]string) error {
	strategy := metadata[strategyKey]
	strategy = strings.ToLower(strategy)
	if strategy == "" {
		strategy = strategyDefault
	} else {
		err := checkKeyIllegal(metadata[strategyKey])
		if err != nil {
			return err
		}
	}

	statesConfigurationLock.Lock()
	defer statesConfigurationLock.Unlock()
	statesConfiguration[storeName] = &StoreConfiguration{keyPrefixStrategy: strategy}
	return nil
}

func GetModifiedStateKey(key, storeName, appID string) (string, error) {
	if err := checkKeyIllegal(key); err != nil {
		return "", err
	}
	stateConfiguration := getStateConfiguration(storeName)
	switch stateConfiguration.keyPrefixStrategy {
	case strategyNone:
		return key, nil
	case strategyStoreName:
		return fmt.Sprintf("%s%s%s", storeName, daprSeparator, key), nil
	case strategyAppid:
		if appID == "" {
			return key, nil
		}
		return fmt.Sprintf("%s%s%s", appID, daprSeparator, key), nil
	case strategyNamespace:
		if appID == "" {
			return key, nil
		}
		if namespace == "" {
			// if namespace is empty, fallback to app id strategy
			return fmt.Sprintf("%s%s%s", appID, daprSeparator, key), nil
		}
		return fmt.Sprintf("%s.%s%s%s", namespace, appID, daprSeparator, key), nil
	default:
		return fmt.Sprintf("%s%s%s", stateConfiguration.keyPrefixStrategy, daprSeparator, key), nil
	}
}

func GetOriginalStateKey(modifiedStateKey string) string {
	splits := strings.Split(modifiedStateKey, daprSeparator)
	if len(splits) <= 1 {
		return modifiedStateKey
	}
	return splits[1]
}

func getStateConfiguration(storeName string) *StoreConfiguration {
	statesConfigurationLock.Lock()
	defer statesConfigurationLock.Unlock()
	c := statesConfiguration[storeName]
	if c == nil {
		c = &StoreConfiguration{keyPrefixStrategy: strategyDefault}
		statesConfiguration[storeName] = c
	}

	return c
}

func checkKeyIllegal(key string) error {
	if strings.Contains(key, daprSeparator) {
		return errors.Errorf("input key/keyPrefix '%s' can't contain '%s'", key, daprSeparator)
	}
	return nil
}
