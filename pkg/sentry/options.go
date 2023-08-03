/*
Copyright 2023 The Dapr Authors
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

package sentry

import (
	"encoding/json"
	"errors"
	"reflect"
	"strconv"
	"time"

	"github.com/mitchellh/mapstructure"
)

func decodeOptions(dest any, opts map[string]string) error {
	decoder, err := mapstructure.NewDecoder(&mapstructure.DecoderConfig{
		Result:           dest,
		WeaklyTypedInput: true,
		DecodeHook: mapstructure.ComposeDecodeHookFunc(
			toTimeDurationHookFunc(),
		),
	})
	if err != nil {
		return err
	}
	return decoder.Decode(opts)
}

type Duration struct {
	time.Duration
}

func (d Duration) MarshalJSON() ([]byte, error) {
	return json.Marshal(d.String())
}

func (d *Duration) UnmarshalJSON(b []byte) error {
	var v interface{}
	if err := json.Unmarshal(b, &v); err != nil {
		return err
	}
	switch value := v.(type) {
	case float64:
		d.Duration = time.Duration(value)

		return nil
	case string:
		var err error
		d.Duration, err = time.ParseDuration(value)
		if err != nil {
			return err
		}

		return nil
	default:
		return errors.New("invalid duration")
	}
}

// This helper function is used to decode durations within a map[string]interface{} into a struct.
// It must be used in conjunction with mapstructure's DecodeHook.
// This is used in utils.DecodeMetadata to decode durations in metadata.
//
//	mapstructure.NewDecoder(&mapstructure.DecoderConfig{
//	   DecodeHook: mapstructure.ComposeDecodeHookFunc(
//	     toTimeDurationHookFunc()),
//	   Metadata: nil,
//			Result:   result,
//	})
func toTimeDurationHookFunc() mapstructure.DecodeHookFunc {
	return func(
		f reflect.Type,
		t reflect.Type,
		data interface{},
	) (interface{}, error) {
		if t != reflect.TypeOf(Duration{}) && t != reflect.TypeOf(time.Duration(0)) {
			return data, nil
		}

		switch f.Kind() {
		case reflect.TypeOf(time.Duration(0)).Kind():
			return data.(time.Duration), nil
		case reflect.String:
			var val time.Duration
			if data.(string) != "" {
				var err error
				val, err = time.ParseDuration(data.(string))
				if err != nil {
					// If we can't parse the duration, try parsing it as int64 seconds
					seconds, errParse := strconv.ParseInt(data.(string), 10, 0)
					if errParse != nil {
						return nil, errors.Join(err, errParse)
					}
					val = time.Duration(seconds * int64(time.Second))
				}
			}
			if t != reflect.TypeOf(Duration{}) {
				return val, nil
			}
			return Duration{Duration: val}, nil
		case reflect.Float64:
			val := time.Duration(data.(float64))
			if t != reflect.TypeOf(Duration{}) {
				return val, nil
			}
			return Duration{Duration: val}, nil
		case reflect.Int64:
			val := time.Duration(data.(int64))
			if t != reflect.TypeOf(Duration{}) {
				return val, nil
			}
			return Duration{Duration: val}, nil
		default:
			return data, nil
		}
	}
}
