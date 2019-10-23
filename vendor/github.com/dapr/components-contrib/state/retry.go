// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package state

import (
	"fmt"
	"time"
)

const (
	FirstWrite  = "first-write"
	LastWrite   = "last-write"
	Strong      = "strong"
	Eventual    = "eventual"
	Exponential = "exponential"
	Linear      = "linear"
)

// CheckSetRequestOptions checks if set request options use supported keywords
func CheckSetRequestOptions(req *SetRequest) error {
	if req.Options.Concurrency != "" && req.Options.Concurrency != FirstWrite && req.Options.Concurrency != LastWrite {
		return fmt.Errorf("unrecognized concurrency model '%s'", req.Options.Concurrency)
	}
	if req.Options.Consistency != "" && req.Options.Consistency != Strong && req.Options.Consistency != Eventual {
		return fmt.Errorf("unrecognized consistency model '%s'", req.Options.Consistency)
	}
	return nil
}

// CheckDeleteRequestOptions checks if delete request options use supported keywords
func CheckDeleteRequestOptions(req *DeleteRequest) error {
	if req.Options.Concurrency != "" && req.Options.Concurrency != FirstWrite && req.Options.Concurrency != LastWrite {
		return fmt.Errorf("unrecognized concurrency model '%s'", req.Options.Concurrency)
	}
	if req.Options.Consistency != "" && req.Options.Consistency != Strong && req.Options.Consistency != Eventual {
		return fmt.Errorf("unrecognized consistency model '%s'", req.Options.Consistency)
	}
	return nil
}

// SetWithRetries handles SetRequest with retries
func SetWithRetries(method func(req *SetRequest) error, req *SetRequest) error {
	switch req.Options.RetryPolicy.Pattern {
	case "":
		fallthrough
	case Linear:
		fallthrough
	case Exponential:
		if req.Options.RetryPolicy.Threshold > 0 {
			duration := req.Options.RetryPolicy.Interval
			for i := 0; i < req.Options.RetryPolicy.Threshold; i++ {
				err := method(req)
				if err == nil {
					return nil
				}
				time.Sleep(duration)
				if req.Options.RetryPolicy.Pattern == Exponential {
					duration *= 2
				}
			}
			return fmt.Errorf("failed to set value after %d retries", req.Options.RetryPolicy.Threshold)
		}
		return method(req)
	default:
		return fmt.Errorf("unrecognized retry patter '%s'", req.Options.RetryPolicy.Pattern)
	}
}

// DeleteWithRetries handles SetRequest with retries
func DeleteWithRetries(method func(req *DeleteRequest) error, req *DeleteRequest) error {
	switch req.Options.RetryPolicy.Pattern {
	case "":
		fallthrough
	case Linear:
		fallthrough
	case Exponential:
		if req.Options.RetryPolicy.Threshold > 0 {
			duration := req.Options.RetryPolicy.Interval
			for i := 0; i < req.Options.RetryPolicy.Threshold; i++ {
				err := method(req)
				if err == nil {
					return nil
				}
				time.Sleep(duration)
				if req.Options.RetryPolicy.Pattern == Exponential {
					duration *= 2
				}
			}
			return fmt.Errorf("failed to set value after %d retries", req.Options.RetryPolicy.Threshold)
		}
		return method(req)
	default:
		return fmt.Errorf("unrecognized retry patter '%s'", req.Options.RetryPolicy.Pattern)
	}
}
