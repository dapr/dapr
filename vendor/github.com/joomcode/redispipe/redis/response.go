package redis

import (
	"fmt"

	"github.com/joomcode/errorx"
)

// AsError casts interface to error (if it is error)
func AsError(v interface{}) error {
	e, _ := v.(error)
	return e
}

// AsErrorx casts interface to *errorx.Error.
// It panics if value is error but not *redis.Error.
func AsErrorx(v interface{}) *errorx.Error {
	e, _ := v.(*errorx.Error)
	if e == nil {
		if _, ok := v.(error); ok {
			panic(fmt.Errorf("result should be either *rediserror.Error, or not error at all, but got %#v", v))
		}
	}
	return e
}

// ScanResponse parses response of Scan command, returns iterator and array of keys.
func ScanResponse(res interface{}) ([]byte, []string, error) {
	if err := AsError(res); err != nil {
		return nil, nil, err
	}
	var ok bool
	var arr []interface{}
	var it []byte
	var keys []interface{}
	var strs []string
	if arr, ok = res.([]interface{}); !ok {
		goto wrong
	}
	if it, ok = arr[0].([]byte); !ok {
		goto wrong
	}
	if keys, ok = arr[1].([]interface{}); !ok {
		goto wrong
	}
	strs = make([]string, len(keys))
	for i, k := range keys {
		var b []byte
		if b, ok = k.([]byte); !ok {
			goto wrong
		}
		strs[i] = string(b)
	}
	return it, strs, nil

wrong:
	return nil, nil, ErrResponseUnexpected.NewWithNoMessage().WithProperty(EKResponse, res)
}

// TransactionResponse parses response of EXEC command, returns array of answers.
func TransactionResponse(res interface{}) ([]interface{}, error) {
	if arr, ok := res.([]interface{}); ok {
		return arr, nil
	}
	if res == nil {
		res = ErrExecEmpty.NewWithNoMessage()
	}
	if _, ok := res.(error); !ok {
		res = ErrResponseUnexpected.NewWithNoMessage().WithProperty(EKResponse, res)
	}
	return nil, res.(error)
}
