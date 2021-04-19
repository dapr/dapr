package retry

import (
	"fmt"
	"testing"

	"github.com/pkg/errors"
	"github.com/stretchr/testify/assert"
)

type retrySettings struct {
	retryStrategy                  string
	retryMaxCount                  int
	retryIntervalInSeconds         int
	expectedRetryStrategy          RetryStrategy
	expectedRetryMaxCount          int
	expectedRetryIntervalInSeconds int
	expectedErrorMessage           string
}

func TestValidateRetryStrategy(t *testing.T) {

	testCasesError := []string{"", "invalidStrategy", "exponEntial", "Linear"}

	t.Run("test valid Retry Strategy", func(t *testing.T) {
		for _, validRetryStrategy := range AllowedRetryStrategies {
			err := validateRetryStrategy(string(validRetryStrategy))
			assert.NoError(t, err, "no error expected")
		}
	})

	t.Run("test invalid Retry Strategy", func(t *testing.T) {
		for _, testCaseError := range testCasesError {
			err := validateRetryStrategy(testCaseError)
			assert.Error(t, err, "expected error")
			expectedErrorMessage := fmt.Sprintf("retry strategy %s is not valid", testCaseError)
			assert.Equal(t, expectedErrorMessage, err.Error(), "expected error string to match")
		}
	})
}

func TestValidateRetryMaxCount(t *testing.T) {

	t.Run("test valid Retry Count", func(t *testing.T) {
		for i := MinRetryMaxCount; i <= MaxRetryMaxCount; i++ {
			err := validateRetryMaxCount(i)
			assert.NoError(t, err, "no error expected")
		}
	})

	t.Run("test invalid Retry Max Count", func(t *testing.T) {
		var emptyMaxCount int
		invalidRetryMaxCounts := []int{MinRetryMaxCount - 1, MaxRetryMaxCount + 1, emptyMaxCount}

		for _, invalidRetryMaxCount := range invalidRetryMaxCounts {
			err := validateRetryMaxCount(invalidRetryMaxCount)
			assert.Error(t, err, "expected error")
			expectedErrorMessage := fmt.Sprintf("retry max count of %d is out of range [%d-%d]", invalidRetryMaxCount, MinRetryMaxCount, MaxRetryMaxCount)
			assert.Equal(t, expectedErrorMessage, err.Error(), "expected error string to match")
		}
	})
}

func TestValidateRetryIntervalInSeconds(t *testing.T) {

	t.Run("test valid Retry Count", func(t *testing.T) {
		for i := MinRetryIntervalInSeconds; i <= MaxRetryIntervalInSeconds; i++ {
			err := validateRetryIntervalInSeconds(i)
			assert.NoError(t, err, "no error expected")
		}
	})

	t.Run("test invalid Retry Interval", func(t *testing.T) {
		var emptyRetryInterval int
		invalidRetryIntervals := []int{MinRetryIntervalInSeconds - 1, MaxRetryIntervalInSeconds + 1, emptyRetryInterval}

		for _, invalidRetryInterval := range invalidRetryIntervals {
			err := validateRetryIntervalInSeconds(invalidRetryInterval)
			assert.Error(t, err, "expected error")
			expectedErrorMessage := fmt.Sprintf("retry interval of %d is out of range [%d-%d]", invalidRetryInterval, MinRetryIntervalInSeconds, MaxRetryIntervalInSeconds)
			assert.Equal(t, expectedErrorMessage, err.Error(), "expected error string to match")
		}
	})
}

func TestValidateRetrySettings(t *testing.T) {
	t.Run("test with valid retry settings", func(t *testing.T) {
		err := validateRetrySettings(DefaultRetrySettings)
		assert.NoError(t, err, "no error expected")
	})
	t.Run("test with invalid retry settings", func(t *testing.T) {
		invalidRetrySettings := []retrySettings{
			{
				retryStrategy:          "",
				retryMaxCount:          MaxRetryMaxCount,
				retryIntervalInSeconds: MinRetryIntervalInSeconds,
				expectedErrorMessage:   "retry strategy  is not valid",
			},
			{
				retryStrategy:          "exponential",
				retryMaxCount:          MaxRetryMaxCount + 1,
				retryIntervalInSeconds: MinRetryIntervalInSeconds,
				expectedErrorMessage:   fmt.Sprintf("retry max count of %d is out of range [%d-%d]", MaxRetryMaxCount+1, MinRetryMaxCount, MaxRetryMaxCount),
			},
			{
				retryStrategy:          "exponential",
				retryMaxCount:          MaxRetryMaxCount,
				retryIntervalInSeconds: MaxRetryIntervalInSeconds + 1,
				expectedErrorMessage:   fmt.Sprintf("retry interval of %d is out of range [%d-%d]", MaxRetryIntervalInSeconds+1, MinRetryIntervalInSeconds, MaxRetryIntervalInSeconds),
			},
		}
		for _, invalidRetrySetting := range invalidRetrySettings {
			retrySettings := RetrySettings{
				RetryStrategy:          AllowedRetryStrategies[invalidRetrySetting.retryStrategy],
				RetryMaxCount:          invalidRetrySetting.retryMaxCount,
				RetryIntervalInSeconds: invalidRetrySetting.retryIntervalInSeconds,
			}
			err := validateRetrySettings(retrySettings)
			assert.Error(t, err, "error expected")
			assert.Equal(t, invalidRetrySetting.expectedErrorMessage, err.Error(), "expected error string to match")
		}
	})
}

func TestNewRetrySettings(t *testing.T) {

	t.Run("test NewRetrySettings with valid Retry Settings", func(t *testing.T) {
		validRetrySettings := []retrySettings{
			{
				retryStrategy:                  "linear",
				expectedRetryStrategy:          linear,
				expectedRetryMaxCount:          defaultRetryMaxCount,
				expectedRetryIntervalInSeconds: defaultRetryIntervalInSeconds,
			},
			{
				retryStrategy:                  "off",
				expectedRetryStrategy:          off,
				expectedRetryMaxCount:          0,
				expectedRetryIntervalInSeconds: 0,
			},
			{
				retryStrategy:                  "off",
				retryMaxCount:                  MaxRetryMaxCount - 1,
				retryIntervalInSeconds:         MaxRetryIntervalInSeconds - 1,
				expectedRetryStrategy:          off,
				expectedRetryMaxCount:          0,
				expectedRetryIntervalInSeconds: 0,
			},
			{
				expectedRetryStrategy:          defaultRetryStrategy,
				expectedRetryMaxCount:          defaultRetryMaxCount,
				expectedRetryIntervalInSeconds: defaultRetryIntervalInSeconds,
			},
			{
				retryMaxCount:                  MaxRetryMaxCount - 1,
				expectedRetryStrategy:          defaultRetryStrategy,
				expectedRetryMaxCount:          MaxRetryMaxCount - 1,
				expectedRetryIntervalInSeconds: defaultRetryIntervalInSeconds,
			},
			{
				retryIntervalInSeconds:         MaxRetryIntervalInSeconds - 1,
				expectedRetryStrategy:          defaultRetryStrategy,
				expectedRetryMaxCount:          defaultRetryMaxCount,
				expectedRetryIntervalInSeconds: MaxRetryIntervalInSeconds - 1,
			},
			{
				retryStrategy:                  "exponential",
				retryMaxCount:                  MinRetryMaxCount + 1,
				expectedRetryStrategy:          exponential,
				expectedRetryMaxCount:          MinRetryMaxCount + 1,
				expectedRetryIntervalInSeconds: defaultRetryIntervalInSeconds,
			},
			{
				retryStrategy:                  "exponential",
				retryIntervalInSeconds:         MinRetryIntervalInSeconds + 1,
				expectedRetryStrategy:          exponential,
				expectedRetryMaxCount:          defaultRetryMaxCount,
				expectedRetryIntervalInSeconds: MinRetryIntervalInSeconds + 1,
			},
			{
				retryMaxCount:                  MaxRetryMaxCount - 1,
				retryIntervalInSeconds:         MaxRetryIntervalInSeconds - 1,
				expectedRetryStrategy:          defaultRetryStrategy,
				expectedRetryMaxCount:          MaxRetryMaxCount - 1,
				expectedRetryIntervalInSeconds: MaxRetryIntervalInSeconds - 1,
			},
			{
				retryStrategy:                  "linear",
				retryMaxCount:                  MaxRetryMaxCount - 1,
				retryIntervalInSeconds:         MaxRetryIntervalInSeconds - 1,
				expectedRetryStrategy:          linear,
				expectedRetryMaxCount:          MaxRetryMaxCount - 1,
				expectedRetryIntervalInSeconds: MaxRetryIntervalInSeconds - 1,
			},
		}

		for _, validRetrySetting := range validRetrySettings {
			var retryStrategy string
			var retryMaxCount, retryIntervalInSeconds int
			if validRetrySetting.retryStrategy != "" {
				retryStrategy = validRetrySetting.retryStrategy
			}
			if validRetrySetting.retryMaxCount != 0 {
				retryMaxCount = validRetrySetting.retryMaxCount
			}
			if validRetrySetting.retryIntervalInSeconds != 0 {
				retryIntervalInSeconds = validRetrySetting.retryIntervalInSeconds
			}
			retrySettings, err := NewRetrySettings(retryStrategy, retryMaxCount, retryIntervalInSeconds)
			assert.NoError(t, err, "no error expected")
			assert.EqualValues(t, validRetrySetting.expectedRetryStrategy, retrySettings.RetryStrategy)
			assert.EqualValues(t, validRetrySetting.expectedRetryMaxCount, retrySettings.RetryMaxCount)
			assert.EqualValues(t, validRetrySetting.expectedRetryIntervalInSeconds, retrySettings.RetryIntervalInSeconds)
		}
	})

	t.Run("test Component with invalid Retry Settings", func(t *testing.T) {
		invalidRetrySettings := []retrySettings{
			{
				retryStrategy:        "invalidRetryStrategy",
				expectedErrorMessage: "retry strategy invalidRetryStrategy is not valid",
			},
			{
				retryMaxCount:        MaxRetryMaxCount + 1,
				expectedErrorMessage: fmt.Sprintf("retry max count of %d is out of range [%d-%d]", MaxRetryMaxCount+1, MinRetryMaxCount, MaxRetryMaxCount),
			},
			{
				retryIntervalInSeconds: MaxRetryIntervalInSeconds + 1,
				expectedErrorMessage:   fmt.Sprintf("retry interval of %d is out of range [%d-%d]", MaxRetryIntervalInSeconds+1, MinRetryIntervalInSeconds, MaxRetryIntervalInSeconds),
			},
		}

		for _, invalidRetrySetting := range invalidRetrySettings {
			var retryStrategy string
			var retryMaxCount, retryIntervalInSeconds int
			if invalidRetrySetting.retryStrategy != "" {
				retryStrategy = invalidRetrySetting.retryStrategy
			}
			if invalidRetrySetting.retryMaxCount != 0 {
				retryMaxCount = invalidRetrySetting.retryMaxCount
			}
			if invalidRetrySetting.retryIntervalInSeconds != 0 {
				retryIntervalInSeconds = invalidRetrySetting.retryIntervalInSeconds
			}
			_, err := NewRetrySettings(retryStrategy, retryMaxCount, retryIntervalInSeconds)
			assert.Error(t, err, "error expected")
			assert.Equal(t, invalidRetrySetting.expectedErrorMessage, err.Error(), "expected error string to match")
		}
	})

}

func TestCustomizeRetrySettings(t *testing.T) {

	baseRetrySettings := DefaultRetrySettings

	t.Run("test NewRetrySettings with valid Retry Settings", func(t *testing.T) {
		validRetrySettings := []retrySettings{
			{
				expectedRetryStrategy:          baseRetrySettings.RetryStrategy,
				expectedRetryMaxCount:          baseRetrySettings.RetryMaxCount,
				expectedRetryIntervalInSeconds: baseRetrySettings.RetryIntervalInSeconds,
			},
			{
				retryStrategy:                  "linear",
				expectedRetryStrategy:          linear,
				expectedRetryMaxCount:          baseRetrySettings.RetryMaxCount,
				expectedRetryIntervalInSeconds: baseRetrySettings.RetryIntervalInSeconds,
			},
			{
				retryStrategy:                  "exponential",
				expectedRetryStrategy:          exponential,
				expectedRetryMaxCount:          baseRetrySettings.RetryMaxCount,
				expectedRetryIntervalInSeconds: baseRetrySettings.RetryIntervalInSeconds,
			},
			{
				retryStrategy:                  "off",
				expectedRetryStrategy:          off,
				expectedRetryMaxCount:          0,
				expectedRetryIntervalInSeconds: 0,
			},
			{
				retryStrategy:                  "linear",
				retryMaxCount:                  MaxRetryMaxCount - 1,
				retryIntervalInSeconds:         MaxRetryIntervalInSeconds - 1,
				expectedRetryStrategy:          linear,
				expectedRetryMaxCount:          MaxRetryMaxCount - 1,
				expectedRetryIntervalInSeconds: MaxRetryIntervalInSeconds - 1,
			},
			{
				retryStrategy:                  "exponential",
				retryMaxCount:                  MaxRetryMaxCount - 1,
				retryIntervalInSeconds:         MaxRetryIntervalInSeconds - 1,
				expectedRetryStrategy:          exponential,
				expectedRetryMaxCount:          MaxRetryMaxCount - 1,
				expectedRetryIntervalInSeconds: MaxRetryIntervalInSeconds - 1,
			},
			{
				retryMaxCount:                  MaxRetryMaxCount - 1,
				expectedRetryStrategy:          baseRetrySettings.RetryStrategy,
				expectedRetryMaxCount:          MaxRetryMaxCount - 1,
				expectedRetryIntervalInSeconds: baseRetrySettings.RetryIntervalInSeconds,
			},
			{
				retryIntervalInSeconds:         MaxRetryIntervalInSeconds - 1,
				expectedRetryStrategy:          baseRetrySettings.RetryStrategy,
				expectedRetryMaxCount:          baseRetrySettings.RetryMaxCount,
				expectedRetryIntervalInSeconds: MaxRetryIntervalInSeconds - 1,
			},
			{
				retryStrategy:                  "exponential",
				retryMaxCount:                  MinRetryMaxCount + 1,
				expectedRetryStrategy:          exponential,
				expectedRetryMaxCount:          MinRetryMaxCount + 1,
				expectedRetryIntervalInSeconds: baseRetrySettings.RetryIntervalInSeconds,
			},
			{
				retryStrategy:                  "exponential",
				retryIntervalInSeconds:         MinRetryIntervalInSeconds + 1,
				expectedRetryStrategy:          exponential,
				expectedRetryMaxCount:          baseRetrySettings.RetryMaxCount,
				expectedRetryIntervalInSeconds: MinRetryIntervalInSeconds + 1,
			},
			{
				retryMaxCount:                  MaxRetryMaxCount - 1,
				retryIntervalInSeconds:         MaxRetryIntervalInSeconds - 1,
				expectedRetryStrategy:          baseRetrySettings.RetryStrategy,
				expectedRetryMaxCount:          MaxRetryMaxCount - 1,
				expectedRetryIntervalInSeconds: MaxRetryIntervalInSeconds - 1,
			},
			{
				retryStrategy:                  "exponential",
				retryMaxCount:                  MaxRetryMaxCount - 1,
				retryIntervalInSeconds:         MaxRetryIntervalInSeconds - 1,
				expectedRetryStrategy:          exponential,
				expectedRetryMaxCount:          MaxRetryMaxCount - 1,
				expectedRetryIntervalInSeconds: MaxRetryIntervalInSeconds - 1,
			},
		}

		for _, validRetrySetting := range validRetrySettings {
			var retryStrategy string
			var retryMaxCount, retryIntervalInSeconds int
			if validRetrySetting.retryStrategy != "" {
				retryStrategy = validRetrySetting.retryStrategy
			}
			if validRetrySetting.retryMaxCount != 0 {
				retryMaxCount = validRetrySetting.retryMaxCount
			}
			if validRetrySetting.retryIntervalInSeconds != 0 {
				retryIntervalInSeconds = validRetrySetting.retryIntervalInSeconds
			}
			retrySettings, err := CustomizeRetrySettings(baseRetrySettings, retryStrategy, retryMaxCount, retryIntervalInSeconds)
			assert.NoError(t, err, "no error expected")
			assert.EqualValues(t, validRetrySetting.expectedRetryStrategy, retrySettings.RetryStrategy)
			assert.EqualValues(t, validRetrySetting.expectedRetryMaxCount, retrySettings.RetryMaxCount)
			assert.EqualValues(t, validRetrySetting.expectedRetryIntervalInSeconds, retrySettings.RetryIntervalInSeconds)
		}
	})

	t.Run("test Component with invalid Retry Settings", func(t *testing.T) {
		invalidRetrySettings := []retrySettings{
			{
				retryStrategy:        "invalidRetryStrategy",
				expectedErrorMessage: "retry strategy invalidRetryStrategy is not valid",
			},
			{
				retryMaxCount:        MaxRetryMaxCount + 1,
				expectedErrorMessage: fmt.Sprintf("retry max count of %d is out of range [%d-%d]", MaxRetryMaxCount+1, MinRetryMaxCount, MaxRetryMaxCount),
			},
			{
				retryIntervalInSeconds: MaxRetryIntervalInSeconds + 1,
				expectedErrorMessage:   fmt.Sprintf("retry interval of %d is out of range [%d-%d]", MaxRetryIntervalInSeconds+1, MinRetryIntervalInSeconds, MaxRetryIntervalInSeconds),
			},
		}

		for _, invalidRetrySetting := range invalidRetrySettings {
			var retryStrategy string
			var retryMaxCount, retryIntervalInSeconds int
			if invalidRetrySetting.retryStrategy != "" {
				retryStrategy = invalidRetrySetting.retryStrategy
			}
			if invalidRetrySetting.retryMaxCount != 0 {
				retryMaxCount = invalidRetrySetting.retryMaxCount
			}
			if invalidRetrySetting.retryIntervalInSeconds != 0 {
				retryIntervalInSeconds = invalidRetrySetting.retryIntervalInSeconds
			}
			_, err := CustomizeRetrySettings(baseRetrySettings, retryStrategy, retryMaxCount, retryIntervalInSeconds)
			assert.Error(t, err, "error expected")
			assert.Equal(t, invalidRetrySetting.expectedErrorMessage, err.Error(), "expected error string to match")
		}
	})

}

func TestRetry(t *testing.T) {
	newOperation := func(consecutiveErrors int) func() error {
		errorsReturned := 0
		operation := func() error {
			if errorsReturned < consecutiveErrors {
				errorsReturned++
				return errors.Errorf("operation error number %d", errorsReturned)
			} else {
				return nil
			}
		}
		return operation
	}

	t.Run("operation fails with empty retry settings", func(t *testing.T) {
		operation := newOperation(0)
		retrySettings := RetrySettings{
			RetryStrategy:          "",
			RetryMaxCount:          0,
			RetryIntervalInSeconds: 0,
		}
		err := Retry(operation, retrySettings)
		expectedErrorMessage := "failed to execute Operation with retry due to invalid retry settings: retry strategy  is not valid"
		assert.Error(t, err, "error expected")
		assert.Equal(t, expectedErrorMessage, err.Error(), "expected error string to match")
	})

	t.Run("operation fails with invalid retry settings", func(t *testing.T) {
		operation := newOperation(0)
		retrySettings := RetrySettings{
			RetryStrategy:          linear,
			RetryMaxCount:          MaxRetryMaxCount + 1,
			RetryIntervalInSeconds: MinRetryIntervalInSeconds,
		}
		err := Retry(operation, retrySettings)
		expectedErrorMessage := fmt.Sprintf("failed to execute Operation with retry due to invalid retry settings: retry max count of %d is out of range [%d-%d]", retrySettings.RetryMaxCount, MinRetryMaxCount, MaxRetryMaxCount)
		assert.Error(t, err, "error expected")
		assert.Equal(t, expectedErrorMessage, err.Error(), "expected error string to match")
	})

	t.Run("operation succeeds the first time", func(t *testing.T) {
		operation := newOperation(0)
		retrySettings := DefaultRetrySettings
		err := Retry(operation, retrySettings)
		assert.NoError(t, err, "no error expected")
	})

	t.Run("operation succeeds in the second retry", func(t *testing.T) {
		operation := newOperation(1)
		retrySettings := DefaultRetrySettings
		err := Retry(operation, retrySettings)
		assert.NoError(t, err, "no error expected")
	})

	t.Run("operation fails after exceeded retry max count", func(t *testing.T) {
		retrySettings := DefaultRetrySettings
		operation := newOperation(retrySettings.RetryMaxCount + 1)
		err := Retry(operation, retrySettings)
		assert.Error(t, err, "error expected")
		expectedErrorMessage := fmt.Sprintf("operation error number %d", retrySettings.RetryMaxCount+1)
		assert.Equal(t, expectedErrorMessage, err.Error(), "expected error string to match")
	})

	t.Run("operation succeeds with exponential retry strategy", func(t *testing.T) {
		retrySettings, _ := NewRetrySettings("exponential", 0, 0)
		operation := newOperation(retrySettings.RetryMaxCount)
		err := Retry(operation, retrySettings)
		assert.NoError(t, err, "no error expected")
	})

}
