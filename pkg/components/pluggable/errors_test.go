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

package pluggable

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestComposeErrorsConverters(t *testing.T) {
	t.Run("compose should not call outer function when the error was converted", func(t *testing.T) {
		outerCalled := 0
		outer := ErrorConverter(func(s status.Status) error {
			outerCalled++
			return s.Err()
		})
		innerCalled := 0
		inner := ErrorConverter(func(s status.Status) error {
			innerCalled++
			return errors.New("")
		})
		composed := outer.Compose(inner)
		err := composed(*status.New(codes.Unknown, ""))
		require.Error(t, err)
		assert.Equal(t, 0, outerCalled)
		assert.Equal(t, 1, innerCalled)
	})
	t.Run("compose should call outer function when the error was not converted", func(t *testing.T) {
		outerCalled := 0
		outer := ErrorConverter(func(s status.Status) error {
			outerCalled++
			return errors.New("my-new-err")
		})
		innerCalled := 0
		inner := ErrorConverter(func(s status.Status) error {
			innerCalled++
			return s.Err()
		})
		composed := outer.Compose(inner)
		err := composed(*status.New(codes.Unknown, ""))
		require.Error(t, err)
		assert.Equal(t, 1, outerCalled)
		assert.Equal(t, 1, innerCalled)
	})
}

func TestErrorsMerge(t *testing.T) {
	t.Run("merge should compose errors with the same grpc code", func(t *testing.T) {
		outerCalled := 0
		errors1 := MethodErrorConverter{
			codes.Canceled: func(s status.Status) error {
				outerCalled++
				return s.Err()
			},
		}
		innerCalled := 0
		errors2 := MethodErrorConverter{
			codes.Canceled: func(s status.Status) error {
				innerCalled++
				return s.Err()
			},
		}

		merged := errors1.Merge(errors2)
		assert.Len(t, merged, 1)
		f, ok := merged[codes.Canceled]
		assert.True(t, ok)
		err := f(*status.New(codes.Unknown, ""))
		require.Error(t, err)
		assert.Equal(t, 1, innerCalled)
		assert.Equal(t, 1, outerCalled)
	})
}
