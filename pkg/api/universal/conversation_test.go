package universal

import (
	"testing"

	"github.com/dapr/components-contrib/conversation"
	"github.com/stretchr/testify/require"
	"github.com/tmc/langchaingo/llms"
)

func TestMapRoleToChatMessageType(t *testing.T) {
	testCases := []struct {
		input    string
		expected llms.ChatMessageType
	}{
		{"user", llms.ChatMessageTypeHuman},
		{"human", llms.ChatMessageTypeHuman},
		{"system", llms.ChatMessageTypeSystem},
		{"assistant", llms.ChatMessageTypeAI},
		{"ai", llms.ChatMessageTypeAI},
		{"tool", llms.ChatMessageTypeTool},
		{"function", llms.ChatMessageTypeFunction},
		{"generic", llms.ChatMessageTypeGeneric},
		{"", llms.ChatMessageTypeHuman},
		{"unknown", llms.ChatMessageTypeHuman},
	}

	for _, tc := range testCases {
		t.Run(tc.input, func(t *testing.T) {
			result := mapRoleToChatMessageType(tc.input)
			require.Equal(t, tc.expected, result)
		})
	}
}

func TestConvertUsageForResponse(t *testing.T) {
	t.Run("nil usage", func(t *testing.T) {
		result := convertUsageForResponse(nil)
		require.Nil(t, result)
	})

	t.Run("basic usage without details", func(t *testing.T) {
		usage := &conversation.Usage{
			CompletionTokens: 100,
			PromptTokens:     50,
			TotalTokens:      150,
		}

		result := convertUsageForResponse(usage)
		require.NotNil(t, result)
		require.Equal(t, int64(100), result.CompletionTokens)
		require.Equal(t, int64(50), result.PromptTokens)
		require.Equal(t, int64(150), result.TotalTokens)
		require.Nil(t, result.CompletionTokensDetails)
		require.Nil(t, result.PromptTokensDetails)
	})

	t.Run("usage with completion tokens details", func(t *testing.T) {
		usage := &conversation.Usage{
			CompletionTokens: 200,
			PromptTokens:     100,
			TotalTokens:      300,
			CompletionTokensDetails: &conversation.CompletionTokensDetails{
				AcceptedPredictionTokens: 10,
				AudioTokens:              5,
				ReasoningTokens:          15,
				RejectedPredictionTokens: 2,
			},
		}

		result := convertUsageForResponse(usage)
		require.NotNil(t, result)
		require.Equal(t, int64(200), result.CompletionTokens)
		require.Equal(t, int64(100), result.PromptTokens)
		require.Equal(t, int64(300), result.TotalTokens)
		require.NotNil(t, result.CompletionTokensDetails)
		require.Equal(t, int64(10), result.CompletionTokensDetails.AcceptedPredictionTokens)
		require.Equal(t, int64(5), result.CompletionTokensDetails.AudioTokens)
		require.Equal(t, int64(15), result.CompletionTokensDetails.ReasoningTokens)
		require.Equal(t, int64(2), result.CompletionTokensDetails.RejectedPredictionTokens)
		require.Nil(t, result.PromptTokensDetails)
	})

	t.Run("usage with prompt tokens details", func(t *testing.T) {
		usage := &conversation.Usage{
			CompletionTokens: 150,
			PromptTokens:     75,
			TotalTokens:      225,
			PromptTokensDetails: &conversation.PromptTokensDetails{
				AudioTokens:  10,
				CachedTokens: 20,
			},
		}

		result := convertUsageForResponse(usage)
		require.NotNil(t, result)
		require.Equal(t, int64(150), result.CompletionTokens)
		require.Equal(t, int64(75), result.PromptTokens)
		require.Equal(t, int64(225), result.TotalTokens)
		require.Nil(t, result.CompletionTokensDetails)
		require.NotNil(t, result.PromptTokensDetails)
		require.Equal(t, int64(10), result.PromptTokensDetails.AudioTokens)
		require.Equal(t, int64(20), result.PromptTokensDetails.CachedTokens)
	})

	t.Run("usage with both details", func(t *testing.T) {
		usage := &conversation.Usage{
			CompletionTokens: 250,
			PromptTokens:     125,
			TotalTokens:      375,
			CompletionTokensDetails: &conversation.CompletionTokensDetails{
				AcceptedPredictionTokens: 20,
				AudioTokens:              8,
				ReasoningTokens:          25,
				RejectedPredictionTokens: 3,
			},
			PromptTokensDetails: &conversation.PromptTokensDetails{
				AudioTokens:  15,
				CachedTokens: 30,
			},
		}

		result := convertUsageForResponse(usage)
		require.NotNil(t, result)
		require.Equal(t, int64(250), result.CompletionTokens)
		require.Equal(t, int64(125), result.PromptTokens)
		require.Equal(t, int64(375), result.TotalTokens)
		require.NotNil(t, result.CompletionTokensDetails)
		require.Equal(t, int64(20), result.CompletionTokensDetails.AcceptedPredictionTokens)
		require.Equal(t, int64(8), result.CompletionTokensDetails.AudioTokens)
		require.Equal(t, int64(25), result.CompletionTokensDetails.ReasoningTokens)
		require.Equal(t, int64(3), result.CompletionTokensDetails.RejectedPredictionTokens)
		require.NotNil(t, result.PromptTokensDetails)
		require.Equal(t, int64(15), result.PromptTokensDetails.AudioTokens)
		require.Equal(t, int64(30), result.PromptTokensDetails.CachedTokens)
	})

	t.Run("usage with zero values", func(t *testing.T) {
		usage := &conversation.Usage{
			CompletionTokens: 0,
			PromptTokens:     0,
			TotalTokens:      0,
		}

		result := convertUsageForResponse(usage)
		require.NotNil(t, result)
		require.Equal(t, int64(0), result.CompletionTokens)
		require.Equal(t, int64(0), result.PromptTokens)
		require.Equal(t, int64(0), result.TotalTokens)
		require.Nil(t, result.CompletionTokensDetails)
		require.Nil(t, result.PromptTokensDetails)
	})
}
