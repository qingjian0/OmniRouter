package deepseekweb

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"strings"
)

var (
	ErrInvalidChatRequest     = errors.New("invalid DeepSeek Web chat request")
	ErrUnsupportedChatRequest = errors.New("unsupported DeepSeek Web chat request")
)

const DefaultAutoHistoryWindow = 20

type ChatCompletionInput struct {
	Model           string
	ModelType       string
	Prompt          string
	ThinkingEnabled bool
	SearchEnabled   bool
	RefFileIDs      []string
}

type chatCompletionRequest struct {
	Model           string          `json:"model"`
	Messages        []chatMessage   `json:"messages"`
	Tools           json.RawMessage `json:"tools,omitempty"`
	ReasoningEffort string          `json:"reasoning_effort"`
	ThinkingEnabled *bool           `json:"thinking_enabled"`
	Thinking        *bool           `json:"thinking"`
	SearchEnabled   *bool           `json:"search_enabled"`
	Search          *bool           `json:"search"`
	WebSearch       *bool           `json:"web_search"`
}

type chatMessage struct {
	Role       string          `json:"role"`
	Content    json.RawMessage `json:"content"`
	Name       string          `json:"name,omitempty"`
	ToolCallID string          `json:"tool_call_id,omitempty"`
	ToolCalls  []chatToolCall  `json:"tool_calls,omitempty"`
}

type chatToolCall struct {
	ID       string           `json:"id"`
	Function chatToolFunction `json:"function"`
}

type chatToolFunction struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

type promptTurn struct {
	role string
	text string
}

// MapChatCompletionInput converts the OpenAI Chat Completions envelope into a
// protocol-neutral DeepSeek Web input. DeepSeek Web accepts one prompt string,
// not an OpenAI messages array. This mapper handles text and prior tool history;
// current tools[] generation remains fail-closed until tool-call response
// parsing and OpenAI tool_calls emission are implemented together.
//
// Prompt serialization follows the MIT deep-proxy reference
// (internal/proxy/translate_request.go). Model option aliases are cross-checked
// against the MIT OmniRoute DeepSeek Web executor. No private HTTP fields,
// credentials, challenge responses, or session IDs are constructed here.
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
	if hasJSONValues(request.Tools) {
		return ChatCompletionInput{}, fmt.Errorf("%w: tools[] response translation is not implemented", ErrUnsupportedChatRequest)
	}

	modelType, modelThinking, modelSearch := resolveDeepSeekModelOptions(request.Model)
	thinkingEnabled := modelThinking || (strings.TrimSpace(request.ReasoningEffort) != "" && !strings.EqualFold(request.ReasoningEffort, "none"))
	if request.ThinkingEnabled != nil {
		thinkingEnabled = *request.ThinkingEnabled
	} else if request.Thinking != nil {
		thinkingEnabled = *request.Thinking
	}
	searchEnabled := modelSearch
	if request.SearchEnabled != nil {
		searchEnabled = *request.SearchEnabled
	} else if request.Search != nil {
		searchEnabled = *request.Search
	} else if request.WebSearch != nil {
		searchEnabled = *request.WebSearch
	}

	prompt, err := messagesToDeepSeekPrompt(request.Messages, DefaultAutoHistoryWindow)
	if err != nil {
		return ChatCompletionInput{}, err
	}
	if prompt == "" {
		return ChatCompletionInput{}, fmt.Errorf("%w: prompt is empty", ErrInvalidChatRequest)
	}

	return ChatCompletionInput{
		Model:           request.Model,
		ModelType:       modelType,
		Prompt:          prompt,
		ThinkingEnabled: thinkingEnabled,
		SearchEnabled:   searchEnabled,
		RefFileIDs:      []string{},
	}, nil
}

func hasJSONValues(raw json.RawMessage) bool {
	value := bytes.TrimSpace(raw)
	return len(value) > 0 && !bytes.Equal(value, []byte("null")) && !bytes.Equal(value, []byte("[]"))
}

func resolveDeepSeekModelOptions(model string) (modelType string, thinking, search bool) {
	name := strings.ToLower(strings.TrimSpace(model))
	modelType = "default"
	if strings.Contains(name, "pro") || strings.Contains(name, "expert") ||
		name == "deepseek-reasoner" || name == "deepseek-r1" {
		modelType = "expert"
	}
	thinking = strings.Contains(name, "r1") || strings.Contains(name, "think") || strings.Contains(name, "reason")
	search = strings.Contains(name, "search")
	return modelType, thinking, search
}

func messagesToDeepSeekPrompt(messages []chatMessage, historyWindow int) (string, error) {
	if len(messages) == 0 {
		return "", nil
	}

	var systemParts []string
	var conversation []promptTurn
	callNameByID := make(map[string]string)
	for index, message := range messages {
		content, err := decodeTextContent(message.Content)
		if err != nil {
			return "", fmt.Errorf("%w: message %d: %v", ErrUnsupportedChatRequest, index, err)
		}

		switch message.Role {
		case "system", "developer":
			if content != "" {
				systemParts = append(systemParts, content)
			}
		case "user":
			if content != "" {
				conversation = append(conversation, promptTurn{role: "user", text: content})
			}
		case "assistant":
			var assistant strings.Builder
			assistant.WriteString(content)
			for _, call := range message.ToolCalls {
				if strings.TrimSpace(call.ID) == "" || strings.TrimSpace(call.Function.Name) == "" || len(call.Function.Arguments) == 0 {
					return "", fmt.Errorf("%w: malformed historical tool call in message %d", ErrUnsupportedChatRequest, index)
				}
				arguments, err := normalizeToolArguments(call.Function.Arguments)
				if err != nil {
					return "", fmt.Errorf("%w: invalid tool arguments in message %d", ErrUnsupportedChatRequest, index)
				}
				callNameByID[call.ID] = call.Function.Name
				if assistant.Len() > 0 {
					assistant.WriteByte('\n')
				}
				assistant.WriteString(fmt.Sprintf("<tool_call name=\"%s\">%s</tool_call>", html.EscapeString(call.Function.Name), arguments))
			}
			if assistant.Len() > 0 {
				conversation = append(conversation, promptTurn{role: "assistant", text: assistant.String()})
			}
		case "tool":
			if content != "" {
				name := callNameByID[message.ToolCallID]
				if name == "" {
					name = message.Name
				}
				if name == "" {
					name = "tool"
				}
				conversation = append(conversation, promptTurn{role: "tool", text: fmt.Sprintf("[Tool result for %s]: %s", name, content)})
			}
		default:
			return "", fmt.Errorf("%w: unsupported message role %q", ErrUnsupportedChatRequest, message.Role)
		}
	}

	parts := make([]string, 0, 2)
	if len(systemParts) > 0 {
		parts = append(parts, "[System]: "+strings.Join(systemParts, "\n\n"))
	}
	if historyWindow <= 0 {
		historyWindow = DefaultAutoHistoryWindow
	}
	if len(conversation) > historyWindow {
		conversation = conversation[len(conversation)-historyWindow:]
	}
	for _, turn := range conversation {
		switch turn.role {
		case "assistant":
			parts = append(parts, "[Assistant]: "+turn.text)
		case "tool":
			parts = append(parts, turn.text)
		default:
			parts = append(parts, "[User]: "+turn.text)
		}
	}
	return strings.Join(parts, "\n\n"), nil
}

func decodeTextContent(raw json.RawMessage) (string, error) {
	value := bytes.TrimSpace(raw)
	if len(value) == 0 || bytes.Equal(value, []byte("null")) {
		return "", nil
	}
	var content string
	if err := json.Unmarshal(value, &content); err != nil {
		return "", fmt.Errorf("content must be a string")
	}
	return content, nil
}

func compactJSON(raw json.RawMessage) []byte {
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return raw
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return raw
	}
	return encoded
}

func normalizeToolArguments(raw json.RawMessage) ([]byte, error) {
	value := bytes.TrimSpace(raw)
	if len(value) == 0 || bytes.Equal(value, []byte("null")) {
		return nil, fmt.Errorf("tool arguments are empty")
	}
	var encoded string
	if err := json.Unmarshal(value, &encoded); err == nil {
		argumentJSON := []byte(strings.TrimSpace(encoded))
		if !json.Valid(argumentJSON) {
			return nil, fmt.Errorf("tool arguments string is not valid JSON")
		}
		return compactJSON(argumentJSON), nil
	}
	if !json.Valid(value) {
		return nil, fmt.Errorf("tool arguments are not valid JSON")
	}
	return compactJSON(value), nil
}
