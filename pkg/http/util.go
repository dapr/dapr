/*
Copyright 2022 The Dapr Authors
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

package http

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"

	contribContentType "github.com/dapr/components-contrib/contenttype"
)

var errContentTypeMismatch = errors.New("error: mismatch between contentType and event")

func ConvertEventToBytes(event interface{}, contentType string) ([]byte, error) {
	if contribContentType.IsBinaryContentType(contentType) {
		// Here the expectation is that for a JSON request, the binary data will be base64 encoded.
		// When content type is given as binary, try to decode base64 as []byte.

		if dataAsString, ok := event.(string); ok {
			decoded, decodeErr := base64.StdEncoding.DecodeString(dataAsString)
			if decodeErr != nil {
				return []byte{}, fmt.Errorf("error: unable to decode base64 application/octect-stream data: %w", decodeErr)
			}

			return decoded, nil
		} else {
			return []byte{}, errContentTypeMismatch
		}
	} else if contribContentType.IsStringContentType(contentType) {
		switch v := event.(type) {
		case string:
			return []byte(v), nil
		default:
			return []byte{}, errContentTypeMismatch
		}
	} else if contribContentType.IsJSONContentType(contentType) || contribContentType.IsCloudEventContentType(contentType) {
		dBytes, err := json.Marshal(event)
		if err != nil {
			return []byte{}, fmt.Errorf("%s: %w", errContentTypeMismatch.Error(), err)
		}
		return dBytes, nil
	}

	return []byte{}, errContentTypeMismatch
}
