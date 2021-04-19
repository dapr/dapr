// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation and Dapr Contributors.
// Licensed under the MIT License.
// ------------------------------------------------------------

package retry

import (
	"fmt"
	"strconv"
	"time"

	"github.com/dapr/dapr/pkg/logger"
	"github.com/pkg/errors"
)

const (
	DefaultLinearBackoffInterval = time.Second
	DefaultLinearRetryCount      = 3
)

//RetryStrategy is the type that represents Retry Policy settings
type RetryStrategy string

const (
	off                           RetryStrategy = "off"
	linear                        RetryStrategy = "linear"
	exponential                   RetryStrategy = "exponential"
	defaultRetryStrategy          RetryStrategy = linear
	defaultRetryMaxCount          int           = 3
	defaultRetryIntervalInSeconds int           = 1
	MinRetryMaxCount              int           = 1
	MaxRetryMaxCount              int           = 5
	MinRetryIntervalInSeconds     int           = 1
	MaxRetryIntervalInSeconds     int           = 3
)

var log = logger.NewLogger("dapr.runtime.retry")

// AllowedRetryStrategies lists allowed Retry Policies supported by the darp Retry
var AllowedRetryStrategies = map[string]RetryStrategy{
	"off":         off,
	"linear":      linear,
	"exponential": exponential,
}

// RetrySettings contains retry settings supported by the dapr Retry
type RetrySettings struct {
	RetryStrategy          RetryStrategy
	RetryMaxCount          int
	RetryIntervalInSeconds int
}

// DefaultRetrySettings object contains the default retry settings
var DefaultRetrySettings = RetrySettings{
	RetryStrategy:          defaultRetryStrategy,
	RetryMaxCount:          defaultRetryMaxCount,
	RetryIntervalInSeconds: defaultRetryIntervalInSeconds,
}

// Operation to be executed by dapr Retry function
// The operation will be retried using a retry policy if it returns an error.
type Operation func() error

// NewRetrySettings returns a valid retry settings object based on the retry settings provided
func NewRetrySettings(retryStrategy string, retryMaxCount int, retryIntervalInSeconds int) (RetrySettings, error) {
	return newRetrySettingsFromBaseline(DefaultRetrySettings, retryStrategy, retryMaxCount, retryIntervalInSeconds)
}

// CustomizeRetrySettings returns a valid retry settings object based on an existing retrySettings and applying a set of custom settings
func CustomizeRetrySettings(retrySettings RetrySettings, retryStrategy string, retryMaxCount int, retryIntervalInSeconds int) (RetrySettings, error) {
	return newRetrySettingsFromBaseline(retrySettings, retryStrategy, retryMaxCount, retryIntervalInSeconds)
}

func newRetrySettingsFromBaseline(baseRetrySettings RetrySettings, retryStrategy string, retryMaxCount int, retryIntervalInSeconds int) (RetrySettings, error) {
	retrySettings := baseRetrySettings
	nilRetrySettings := RetrySettings{
		RetryStrategy:          off,
		RetryMaxCount:          0,
		RetryIntervalInSeconds: 0,
	}
	if retryStrategy != "" {
		err := validateRetryStrategy(string(retryStrategy))
		if err == nil {
			log.Debugf("setting retry strategy %s", retryStrategy)
			retrySettings.RetryStrategy = AllowedRetryStrategies[retryStrategy]
		} else {
			return nilRetrySettings, err
		}
	} else {
		log.Debugf("no retry strategy provided. setting default %s", string(baseRetrySettings.RetryStrategy))
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
		log.Debugf("no retry max count provided. setting default %d", baseRetrySettings.RetryMaxCount)
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
		log.Debugf("no retry interval provided. setting default %s", baseRetrySettings.RetryIntervalInSeconds)
	}

	return retrySettings, nil
}

func validateRetrySettings(retrySettings RetrySettings) error {
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
func Retry(operation Operation, retrySettings RetrySettings) error {
	err := validateRetrySettings(retrySettings)
	if err != nil {
		return errors.Errorf("failed to execute Operation with retry due to invalid retry settings: %s", err.Error())
	}
	err = operation()
	if err != nil && retrySettings.RetryStrategy != off {
		log.Infof("Operation failed. Will retry as per the retrySettings provided: retryStrategy=%s, retryMaxCount=%d, retryIntervalInSeconds=%d", retrySettings.RetryStrategy, retrySettings.RetryMaxCount, retrySettings.RetryIntervalInSeconds)
		ch := make(chan error, 1)
		retryFn := func(c chan error) {
			backoff := retrySettings.RetryIntervalInSeconds
			elapsedTime := 0
			retryOperationOK := false
			for retryCount := 1; retryCount <= retrySettings.RetryMaxCount; retryCount++ {
				if retrySettings.RetryStrategy == exponential {
					backoff = retryCount * retrySettings.RetryIntervalInSeconds
				}
				log.Infof("retrying operation in %d seconds", backoff)
				elapsedTime = elapsedTime + backoff
				timer, _ := time.ParseDuration(fmt.Sprintf("%ss", strconv.Itoa(backoff)))
				time.Sleep(timer)
				err = operation()
				if err == nil {
					retryOperationOK = true
					log.Infof("retry operation succeeded after %d seconds and %d retries", elapsedTime, retryCount)
					c <- nil
					break
				}
				log.Infof("retry operation failed with error: %s", err.Error())
			}
			if !retryOperationOK {
				log.Warnf("completed retry operation as failed after %d seconds and %d retries", elapsedTime, retrySettings.RetryMaxCount)
				c <- err
			}
		}
		go retryFn(ch)
		err = <-ch
	}
	return err
}
