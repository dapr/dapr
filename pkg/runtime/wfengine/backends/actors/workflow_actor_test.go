package actors

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRecoverableError(t *testing.T) {
	err := newRecoverableError(errExecutionAborted)

	var recoverableErr *recoverableError

	require.ErrorAs(t, err, &recoverableErr)

	require.Equal(t, errExecutionAborted.Error(), recoverableErr.Error())
}
