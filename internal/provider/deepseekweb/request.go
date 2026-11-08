package deepseekweb

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

var (
	ErrInvalidChatRequest     = errors.New("invalid DeepSeek Web chat request")
	ErrUnsupportedChatRequest = errors.New("unsupported DeepSeek Web chat request")
)

type ChatCompletionInput struct {
	Model           string
	ModelType       string
	Prompt          string
	ThinkingEnabled bool
	SearchEnabled   bool
	RefFileIDs      []string
}

type chatCompletionRequest struct {
	Model           string        `json:"model"`
	Messages        []chatMessage `json:"messages"`
	ReasoningEffort string        `json:"reasoning_effort"`
	ThinkingEnabled *bool         `json:"thinking_enabled"`
	SearchEnabled   *bool         `json:"search_enabled"`
}

type chatMessage struct {
	Role      string          `json:"role"`
	Content   json.RawMessage `json:"content"`
	Name      string          `json:"name,omitempty"`
	ToolCalls json.RawMessage `json:"tool_calls,omitempty"`
}

// MapChatCompletionInput converts the text-only OpenAI Chat Completions
// envelope into a protocol-neutral DeepSeek Web completion input. It does not
// build an upstream HTTP payload: session IDs, parent IDs, auth headers, and
// private wire fields remain the responsibility of a fixture-verified runtime.
//
// The role-tagged prompt projection follows the MIT-licensed deep-proxy
// reference (internal/proxy/translate_request.go), while model-option aliases
// are cross-checked against that adapter and the MIT OmniRoute DeepSeek Web
// executor. Unsupported multimodal/tool structures fail closed.
func MapChatCompletionInput(raw []byte) (ChatCompletionInput, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	var request chatCompletionRequest
	if err := decoder.Decode(&request); err != nil {
		return ChatCompletionInput{}, fmt.Errorf("%w: decode JSON: %v", ErrInvalidChatRequest, err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return ChatCompletionInput{}, fmt.Errorf("%w: trailing JSON value", ErrInvalidChatRequest)
	}

	request.Model = strings.TrimSpace(request.Model)
	if request.Model == "" || len(request.Messages) == 0 {
		return ChatCompletionInput{}, fmt.Errorf("%w: model and at least one message are required", ErrInvalidChatRequest)
	}

	modelType := resolveDeepSeekModelType(request.Model)
	thinkingEnabled := modelType == "expert" || (strings.TrimSpace(request.ReasoningEffort) != "" && !strings.EqualFold(request.ReasoningEffort, "none"))
	if request.ThinkingEnabled != nil {
		thinkingEnabled = *request.ThinkingEnabled
	}
	searchEnabled := false
	if request.SearchEnabled != nil {
		searchEnabled = *request.SearchEnabled
	}

	var prompt strings.Builder
	for index, message := range request.Messages {
		toolCalls := bytes.TrimSpace(message.ToolCalls)
		if len(toolCalls) > 0 && !bytes.Equal(toolCalls, []byte("null")) && !bytes.Equal(toolCalls, []byte("[]")) {
			return ChatCompletionInput{}, fmt.Errorf("%w: tool_calls are not supported (message %d)", ErrUnsupportedChatRequest, index)
		}
		contentRaw := bytes.TrimSpace(message.Content)
		var content string
		if len(contentRaw) == 0 || bytes.Equal(contentRaw, []byte("null")) {
			return ChatCompletionInput{}, fmt.Errorf("%w: text content is required (message %d)", ErrUnsupportedChatRequest, index)
		}
		if err := json.Unmarshal(contentRaw, &content); err != nil {
			return ChatCompletionInput{}, fmt.Errorf("%w: message %d content must be a string", ErrUnsupportedChatRequest, index)
		}

		var label string
		switch message.Role {
		case "system":
			label = "System"
		case "user":
			label = "User"
		case "assistant":
			label = "Assistant"
		default:
			return ChatCompletionInput{}, fmt.Errorf("%w: unsupported message role %q", ErrUnsupportedChatRequest, message.Role)
		}
		if index > 0 {
			prompt.WriteString("\n\n")
		}
		prompt.WriteByte('[')
		prompt.WriteString(label)
		prompt.WriteString("]: ")
		prompt.WriteString(content)
	}

	return ChatCompletionInput{
		Model:           request.Model,
		ModelType:       modelType,
		Prompt:          prompt.String(),
		ThinkingEnabled: thinkingEnabled,
		SearchEnabled:   searchEnabled,
		RefFileIDs:      []string{},
	}, nil
}

func resolveDeepSeekModelType(model string) string {
	switch strings.ToLower(strings.TrimSpace(model)) {
	case "deepseek-reasoner", "deepseek-r1", "deepseek-expert":
		return "expert"
	default:
		return "default"
	}
}
