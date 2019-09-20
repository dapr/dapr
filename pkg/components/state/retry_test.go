package state

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestRetriPolicies(t *testing.T) {
	t.Run("set with no options", func(t *testing.T) {
		counter := 0
		SetWithRetries(func(req *SetRequest) error {
			counter++
			return nil
		}, &SetRequest{})
		assert.Equal(t, 1, counter, "should execute only once")
	})

	t.Run("set with no retry policies", func(t *testing.T) {
		counter := 0
		SetWithRetries(func(req *SetRequest) error {
			counter++
			return nil
		}, &SetRequest{
			Options: SetStateOption{},
		})
		assert.Equal(t, 1, counter, "should execute only once")
	})

	t.Run("set with empty retry policies", func(t *testing.T) {
		counter := 0
		SetWithRetries(func(req *SetRequest) error {
			counter++
			return nil
		}, &SetRequest{
			Options: SetStateOption{
				RetryPolicy: RetryPolicy{
					Interval:  0,
					Threshold: 0,
					Pattern:   "",
				},
			},
		})
		assert.Equal(t, 1, counter, "should execute only once")
	})

	t.Run("bad policy", func(t *testing.T) {
		ret := SetWithRetries(func(req *SetRequest) error {
			return nil
		}, &SetRequest{
			Options: SetStateOption{
				RetryPolicy: RetryPolicy{
					Interval:  100,
					Threshold: 3,
					Pattern:   "dummy",
				},
			},
		})
		assert.NotNil(t, ret, "should reject policy")
	})

	t.Run("liner retry 3 times", func(t *testing.T) {
		counter := 0
		SetWithRetries(func(req *SetRequest) error {
			counter++
			if counter < 3 {
				return fmt.Errorf("BAD")
			}
			return nil
		}, &SetRequest{
			Options: SetStateOption{
				RetryPolicy: RetryPolicy{
					Interval:  100,
					Threshold: 3,
					Pattern:   "linear",
				},
			},
		})
		assert.Equal(t, 3, counter, "should execute 3 times")
	})

}
