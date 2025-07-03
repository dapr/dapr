# Dapr Conversation Integration Tests

This directory contains comprehensive integration tests for the Dapr conversation API, including support for streaming, tool calling, PII scrubbing, and real LLM provider testing.

## Test Structure

```
conversation/
├── grpc/           # gRPC-specific tests
│   ├── basic.go           # Basic conversation functionality
│   ├── scrubpii.go        # PII scrubbing tests
│   ├── streaming.go       # Streaming conversation tests
│   ├── tool_calling.go    # Tool calling via HTTP (mixed test)
│   ├── tool_calling_debug.go  # Advanced tool calling scenarios
│   └── simple_debug.go    # Simple tool calling debugging
├── http/           # HTTP-specific tests
│   ├── basic.go           # Basic HTTP conversation
│   ├── scrubpii.go        # HTTP PII scrubbing
│   └── tool_calling.go    # HTTP tool calling tests
└── README.md       # This documentation
```

## Running Tests

### Basic Test Execution

```bash
# Run ALL conversation tests (echo component only)
CGO_ENABLED=1 go test ./tests/integration -timeout=20m -count=1 -v -tags="integration" -integration-parallel=false -focus="daprd/conversation"

# Run only gRPC tests
CGO_ENABLED=1 go test ./tests/integration -timeout=20m -count=1 -v -tags="integration" -integration-parallel=false -focus="daprd/conversation/grpc"

# Run only HTTP tests
CGO_ENABLED=1 go test ./tests/integration -timeout=20m -count=1 -v -tags="integration" -integration-parallel=false -focus="daprd/conversation/http"

# Run specific test suites
CGO_ENABLED=1 go test ./tests/integration -timeout=20m -count=1 -v -tags="integration" -integration-parallel=false -focus="daprd/conversation/grpc/streaming"
CGO_ENABLED=1 go test ./tests/integration -timeout=20m -count=1 -v -tags="integration" -integration-parallel=false -focus="daprd/conversation.*toolCalling"
```

## Testing with Real LLM Providers

### API Key Setup

Create a `.local/.env` file in the Dapr repository root with your API keys:

```bash
# Create the .local directory if it doesn't exist
mkdir -p .local

# Create the .env file with your API keys
cat > .local/.env << 'EOF'
# OpenAI Configuration
OPENAI_API_KEY=sk-your-openai-api-key-here

# Anthropic Configuration  
ANTHROPIC_API_KEY=sk-ant-your-anthropic-api-key-here

# Google AI Configuration
GOOGLE_AI_API_KEY=your-google-ai-api-key-here

# Optional: Other providers (if supported)
# AZURE_OPENAI_API_KEY=your-azure-key
# AZURE_OPENAI_ENDPOINT=https://your-resource.openai.azure.com/
EOF
```

### Environment Variable Mapping

The tests use these environment variable names:

| Provider | Environment Variable | Component Name | Notes |
|----------|---------------------|----------------|-------|
| OpenAI | `OPENAI_API_KEY` | `openai` | GPT models |
| Anthropic | `ANTHROPIC_API_KEY` | `anthropic` | Claude models |
| Google AI | `GOOGLE_API_KEY` | `googleai` | Gemini models |

**Important**: The `.env` file uses `GOOGLE_AI_API_KEY`, but tests expect `GOOGLE_API_KEY`. The test command handles this mapping automatically.

### Running Tests with Real Providers

```bash
# Load environment variables and run all conversation tests
source .local/.env && GOOGLE_API_KEY=$GOOGLE_AI_API_KEY CGO_ENABLED=1 go test ./tests/integration -timeout=20m -count=1 -v -tags="integration" -integration-parallel=false -focus="daprd/conversation"

# Run only streaming tests with all providers
source .local/.env && OPENAI_API_KEY=$OPENAI_API_KEY ANTHROPIC_API_KEY=$ANTHROPIC_API_KEY GOOGLE_API_KEY=$GOOGLE_AI_API_KEY CGO_ENABLED=1 go test ./tests/integration -timeout=20m -count=1 -v -tags="integration" -integration-parallel=false -focus="daprd/conversation/grpc/streaming"

# Run only tool calling tests with real providers
source .local/.env && OPENAI_API_KEY=$OPENAI_API_KEY ANTHROPIC_API_KEY=$ANTHROPIC_API_KEY GOOGLE_API_KEY=$GOOGLE_AI_API_KEY CGO_ENABLED=1 go test ./tests/integration -timeout=20m -count=1 -v -tags="integration" -integration-parallel=false -focus="daprd/conversation.*toolCalling"
```

### Quick Test Command (Recommended)

For convenience, create this alias in your shell:

```bash
# Add to your ~/.bashrc or ~/.zshrc
alias test-conversation='source .local/.env && OPENAI_API_KEY=$OPENAI_API_KEY ANTHROPIC_API_KEY=$ANTHROPIC_API_KEY GOOGLE_API_KEY=$GOOGLE_AI_API_KEY CGO_ENABLED=1 go test ./tests/integration -timeout=20m -count=1 -v -tags="integration" -integration-parallel=false -focus="daprd/conversation"'

# Then simply run:
test-conversation
```

## Test Behavior with API Keys

### Echo Component (Always Available)
- **Purpose**: Predictable test component for basic functionality
- **Behavior**: Echoes last user message, generates mock tool calls for weather queries
- **finishReason**: Always provides `"stop"` or `"tool_calls"`
- **Usage**: Provides realistic token calculations

### Real LLM Providers (API Key Required)

#### OpenAI (`OPENAI_API_KEY`)
- **Model**: `gpt-3.5-turbo` (configurable)
- **Streaming**: ✅ Full streaming support with word-level chunks
- **Tool Calling**: ✅ Native function calling support
- **PII Scrubbing**: ✅ Real-time scrubbing in streaming mode
- **finishReason**: `"stop"`, `"length"`, `"tool_calls"`

#### Anthropic (`ANTHROPIC_API_KEY`)
- **Model**: Claude models
- **Streaming**: ✅ Streaming support with phrase-level chunks
- **Tool Calling**: ✅ Native tool calling (may require multiple outputs)
- **PII Scrubbing**: ✅ Real-time scrubbing
- **finishReason**: `"stop"`, `"max_tokens"`, `"tool_calls"`
- **Note**: Streaming tool calling has known limitations

#### Google AI (`GOOGLE_API_KEY`)
- **Model**: Gemini models
- **Streaming**: ✅ Streaming support
- **Tool Calling**: ✅ Native function calling
- **PII Scrubbing**: ✅ Real-time scrubbing
- **finishReason**: `"stop"`, `"tool_calls"`

## Test Results Interpretation

### Successful Test Run
```
=== RUN   Test_Integration/daprd/conversation/grpc/streaming/run/streaming_with_openai_live
    streaming.go:418: Received chunk: "Hello"
    streaming.go:418: Received chunk: " from"
    streaming.go:418: Received chunk: " OpenAI"
    streaming.go:423: Stream completed with usage: prompt_tokens:19 completion_tokens:6 total_tokens:25
    streaming.go:434: Full openai response: "Hello from OpenAI streaming!"
```

### Skipped Tests (Missing API Keys)
```
=== SKIP   Test_Integration/daprd/conversation/grpc/streaming/run/streaming_with_openai_live
    streaming.go:XXX: OPENAI_API_KEY not set, skipping live OpenAI test
```

## Troubleshooting

### Common Issues

1. **API Key Not Found**
   ```
   SKIP: OPENAI_API_KEY not set, skipping live OpenAI test
   ```
   **Solution**: Ensure your `.local/.env` file contains the correct API key and you're sourcing it.

2. **Invalid API Key**
   ```
   ERROR: 401 Unauthorized
   ```
   **Solution**: Verify your API key is correct and has sufficient credits/permissions.

3. **Rate Limiting**
   ```
   ERROR: 429 Too Many Requests
   ```
   **Solution**: Wait a moment and retry, or check your API usage limits.

4. **Network Issues**
   ```
   ERROR: context deadline exceeded
   ```
   **Solution**: Check your internet connection and increase timeout if needed.

### Debug Mode

For detailed debugging, add debug flags:

```bash
source .local/.env && OPENAI_API_KEY=$OPENAI_API_KEY CGO_ENABLED=1 go test ./tests/integration -timeout=20m -count=1 -v -tags="integration" -integration-parallel=false -focus="daprd/conversation" -args -test.v=true
```

### Component Configuration

The tests automatically configure components with your API keys. Here's what gets created:

```yaml
# OpenAI Component (when OPENAI_API_KEY is set)
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: openai
spec:
  type: conversation.openai
  version: v1
  metadata:
  - name: key
    value: $OPENAI_API_KEY
  - name: model
    value: "gpt-3.5-turbo"

# Anthropic Component (when ANTHROPIC_API_KEY is set)
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: anthropic
spec:
  type: conversation.anthropic
  version: v1
  metadata:
  - name: key
    value: $ANTHROPIC_API_KEY

# Google AI Component (when GOOGLE_API_KEY is set)
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: googleai
spec:
  type: conversation.googleai
  version: v1
  metadata:
  - name: key
    value: $GOOGLE_API_KEY
```

## Test Coverage Matrix

| Feature | Echo | OpenAI | Anthropic | Google AI |
|---------|------|--------|-----------|-----------|
| Basic Conversation | ✅ | ✅ | ✅ | ✅ |
| Streaming | ✅ | ✅ | ✅ | ✅ |
| Tool Calling | ✅ | ✅ | ✅ | ✅ |
| Streaming + Tools | ✅ | ✅ | ⚠️* | ✅ |
| PII Scrubbing | ✅ | ✅ | ✅ | ✅ |
| HTTP API | ✅ | ✅ | ✅ | ✅ |
| gRPC API | ✅ | ✅ | ✅ | ✅ |

*⚠️ Anthropic streaming tool calling has known limitations in components-contrib*

## Performance Expectations

### Test Execution Times
- **Echo tests**: ~0.01s per test (instant)
- **Real LLM tests**: ~0.5-2.0s per test (network dependent)
- **Complete suite**: ~15s with API keys, ~5s echo-only

### API Usage
- **OpenAI**: ~10-100 tokens per test
- **Anthropic**: ~10-100 tokens per test  
- **Google AI**: ~5-50 tokens per test

**Estimated cost per full test run**: < $0.01 USD

## CI/CD Integration

For continuous integration, set environment variables in your CI system:

```yaml
# GitHub Actions example
env:
  OPENAI_API_KEY: ${{ secrets.OPENAI_API_KEY }}
  ANTHROPIC_API_KEY: ${{ secrets.ANTHROPIC_API_KEY }}
  GOOGLE_API_KEY: ${{ secrets.GOOGLE_API_KEY }}
```

Tests will automatically skip provider-specific tests if API keys are not available, ensuring CI doesn't fail due to missing credentials. 