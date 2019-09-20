package sns

import (
	"testing"

	"github.com/actionscore/actions/pkg/components/bindings"
	"github.com/stretchr/testify/assert"
)

func TestParseMetadata(t *testing.T) {
	m := bindings.Metadata{}
	m.Properties = map[string]string{"TopicArn": "a", "Region": "a", "AccessKey": "a", "SecretKey": "a"}
	s := AWSSNS{}
	snsM, err := s.parseMetadata(m)
	assert.Nil(t, err)
	assert.Equal(t, "a", snsM.TopicArn)
	assert.Equal(t, "a", snsM.Region)
	assert.Equal(t, "a", snsM.AccessKey)
	assert.Equal(t, "a", snsM.SecretKey)
}
