// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation and Dapr Contributors.
// Licensed under the MIT License.
// ------------------------------------------------------------

package retry

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/cenkalti/backoff/v4"
	"github.com/dapr/kit/logger"
	"github.com/pkg/errors"
)

const (
	DefaultLinearBackoffInterval = time.Second
	DefaultLinearRetryCount      = 3
)

// Strategy is the type that represents Retry Policy settings
type Strategy string

const (
	off                           Strategy = "off"
	linear                        Strategy = "linear"
	exponential                   Strategy = "exponential"
	defaultRetryStrategy          Strategy = linear
	defaultRetryMaxCount          int      = 3
	defaultRetryIntervalInSeconds int      = 1
	MinRetryMaxCount              int      = 1
	MaxRetryMaxCount              int      = 5
	MinRetryIntervalInSeconds     int      = 1
	MaxRetryIntervalInSeconds     int      = 3
)

// AllowedRetryStrategies lists allowed Retry Policies supported by the darp Retry
var AllowedRetryStrategies = map[string]Strategy{
	"off":         off,
	"linear":      linear,
	"exponential": exponential,
}

// Settings contains retry settings supported by the dapr Retry
type Settings struct {
	RetryStrategy          Strategy
	RetryMaxCount          int
	RetryIntervalInSeconds int
}

// DefaultRetrySettings object contains the default retry settings
var DefaultRetrySettings = Settings{
	RetryStrategy:          defaultRetryStrategy,
	RetryMaxCount:          defaultRetryMaxCount,
	RetryIntervalInSeconds: defaultRetryIntervalInSeconds,
}

var nilRetrySettings = Settings{
	RetryStrategy:          off,
	RetryMaxCount:          0,
	RetryIntervalInSeconds: 0,
}

// NewRetrySettings returns a valid retry settings object based on the retry settings provided
func NewRetrySettings(retryStrategy string, retryMaxCount int, retryIntervalInSeconds int, log logger.Logger) (Settings, error) {
	retrySettings := DefaultRetrySettings
	if retryStrategy != "" {
		err := validateRetryStrategy(retryStrategy)
		if err == nil {
			log.Debugf("setting retry strategy %s", retryStrategy)
			retrySettings.RetryStrategy = AllowedRetryStrategies[retryStrategy]
		} else {
			return nilRetrySettings, err
		}
	} else {
		log.Debugf("no retry strategy provided. setting default %s", string(retrySettings.RetryStrategy))
	}

	if retrySettings.RetryStrategy == off {
		return nilRetrySettings, nil
	}

	if retryMaxCount != 0 {
		err := validateRetryMaxCount(retryMaxCount)
		if err == nil {
			log.Debugf("setting retry max count %d", retryMaxCount)
			retrySettings.RetryMaxCount = retryMaxCount
		} else {
			return nilRetrySettings, err
		}
	} else {
		log.Debugf("no retry max count provided. setting default %d", retrySettings.RetryMaxCount)
	}

	if retryIntervalInSeconds != 0 {
		err := validateRetryIntervalInSeconds(retryIntervalInSeconds)
		if err == nil {
			log.Debugf("setting retry interval %d", retryIntervalInSeconds)
			retrySettings.RetryIntervalInSeconds = retryIntervalInSeconds
		} else {
			return nilRetrySettings, err
		}
	} else {
		log.Debugf("no retry interval provided. setting default %s", retrySettings.RetryIntervalInSeconds)
	}

	return retrySettings, nil
}

// CustomizeRetrySettings returns a valid retry settings object based on an existing retrySettings and applying a set of custom settings
func CustomizeRetrySettings(retrySettings Settings, customRetrySettings Settings, log logger.Logger) (Settings, error) {
	log.Debugf("customizing base retry settings")
	err := validateRetrySettings(retrySettings)
	if err != nil {
		return nilRetrySettings, errors.Errorf("base retry settings provided are invalid: %s", err.Error())
	}

	if customRetrySettings.RetryStrategy != "" {
		log.Debugf("customizing retry strategy to %s", customRetrySettings.RetryStrategy)
		if customRetrySettings.RetryStrategy == off {
			return nilRetrySettings, nil
		}
		retrySettings.RetryStrategy = customRetrySettings.RetryStrategy
	} else {
		log.Debugf("no custom retry strategy provided. applying baseline %d", retrySettings.RetryStrategy)
	}

	if customRetrySettings.RetryMaxCount != 0 {
		err := validateRetryMaxCount(customRetrySettings.RetryMaxCount)
		if err != nil {
			return nilRetrySettings, errors.Errorf("custom retry settings provided are invalid: %s", err.Error())
		}
		log.Debugf("customizing retry max count to %d", customRetrySettings.RetryMaxCount)
		retrySettings.RetryMaxCount = customRetrySettings.RetryMaxCount
	} else {
		log.Debugf("no custom retry max count provided. applying baseline %d", retrySettings.RetryMaxCount)
	}

	if customRetrySettings.RetryIntervalInSeconds != 0 {
		err := validateRetryIntervalInSeconds(customRetrySettings.RetryIntervalInSeconds)
		if err != nil {
			return nilRetrySettings, errors.Errorf("custom retry settings provided are invalid: %s", err.Error())
		}
		log.Debugf("customizing retry interval to %d", customRetrySettings.RetryIntervalInSeconds)
		retrySettings.RetryIntervalInSeconds = customRetrySettings.RetryIntervalInSeconds
	} else {
		log.Debugf("no custom retry interval provided. applying baseline %s", retrySettings.RetryIntervalInSeconds)
	}

	return retrySettings, nil
}

func validateRetrySettings(retrySettings Settings) error {
	err := validateRetryStrategy(string(retrySettings.RetryStrategy))
	if err == nil {
		err = validateRetryMaxCount(retrySettings.RetryMaxCount)
	}
	if err == nil {
		err = validateRetryIntervalInSeconds(retrySettings.RetryIntervalInSeconds)
	}
	return err
}

func validateRetryStrategy(retryStrategy string) error {
	for _, allowedRetryStrategy := range AllowedRetryStrategies {
		if retryStrategy == string(allowedRetryStrategy) {
			return nil
		}
	}
	return errors.Errorf("retry strategy %s is not valid", retryStrategy)
}

func validateRetryMaxCount(retryMaxCount int) error {
	if retryMaxCount > MaxRetryMaxCount || retryMaxCount < MinRetryMaxCount {
		return errors.Errorf("retry max count of %d is out of range [%d-%d]", retryMaxCount, MinRetryMaxCount, MaxRetryMaxCount)
	}
	return nil
}

func validateRetryIntervalInSeconds(retryIntervalInSeconds int) error {
	_, isValidDuration := time.ParseDuration(fmt.Sprintf("%ss", strconv.Itoa(retryIntervalInSeconds)))
	if isValidDuration != nil {
		return errors.Errorf("retry interval value provided of %d fails to convert to a valid duration with %s", retryIntervalInSeconds, isValidDuration.Error())
	}
	if retryIntervalInSeconds > MaxRetryIntervalInSeconds || retryIntervalInSeconds < MinRetryIntervalInSeconds {
		return errors.Errorf("retry interval of %d is out of range [%d-%d]", retryIntervalInSeconds, MinRetryIntervalInSeconds, MaxRetryIntervalInSeconds)
	}
	return nil
}

// Retry executes an Operation as per the Retry Policy specified and till it succeeds or the max number of retries specified in the retry settings provided
func Retry(operation backoff.Operation, retrySettings Settings, notify backoff.Notify, recovered func(), log logger.Logger) error {
	err := validateRetrySettings(retrySettings)
	if err != nil {
		return errors.Errorf("failed to execute Operation with retry due to invalid retry settings: %s", err.Error())
	}
	err = operation()
	if err != nil && retrySettings.RetryStrategy != off {
		var bo backoff.BackOff
		interval, _ := time.ParseDuration(fmt.Sprintf("%ss", strconv.Itoa(retrySettings.RetryIntervalInSeconds)))
		switch retrySettings.RetryStrategy {
		case linear:
			{
				ebo := backoff.NewConstantBackOff(interval)
				bo = backoff.WithMaxRetries(ebo, uint64(retrySettings.RetryMaxCount)-1)
			}
		case exponential:
			{
				ebo := backoff.NewExponentialBackOff()
				ebo.InitialInterval = interval
				bo = backoff.WithMaxRetries(ebo, uint64(retrySettings.RetryMaxCount)-1)
			}
		}
		bo = backoff.WithContext(bo, context.Background())

		if notify == nil {
			notify = func(e error, d time.Duration) {
				log.Debugf("retry operation failed with error: %s", e.Error())
			}
		}

		if recovered == nil {
			recovered = func() {
				log.Debugf("retry operation succeeded after it previously failed")
			}
		}

		return retryNotifyRecover(operation, bo, notify, recovered)
	}
	return err
}

func retryNotifyRecover(operation backoff.Operation, b backoff.BackOff, notify backoff.Notify, recovered func()) error {
	var notified bool

	return backoff.RetryNotify(func() error {
		err := operation()

		if err == nil && notified {
			notified = false
			recovered()
		}

		return err
	}, b, func(err error, d time.Duration) {
		if !notified {
			notify(err, d)
			notified = true
		}
	})
}
