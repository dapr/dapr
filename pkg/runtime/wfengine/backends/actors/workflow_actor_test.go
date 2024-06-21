package actors

import (
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRecoverableError(t *testing.T) {
	err := newRecoverableError(errExecutionAborted)

	var recoverableErr *recoverableError

	require.True(t, errors.As(err, &recoverableErr))

	require.Equal(t, errExecutionAborted.Error(), recoverableErr.Error())
}

func TestNonRecoverableError(t *testing.T) {
	err := fmt.Errorf("foobaz %w", errExecutionAborted)

	var recoverableErr *recoverableError

	require.False(t, errors.As(err, &recoverableErr))
}
