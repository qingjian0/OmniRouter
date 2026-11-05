// Package anthropic translates the supported Anthropic Messages wire format to
// and from OpenAI Chat Completions. Tool calls and tool results are preserved;
// unsupported content block types are rejected rather than silently dropped.
package anthropic

import (
	"encoding/json"
	"fmt"
	"strings"
)


type requestMetadata struct {
	Model  string `json:"model"`
	Stream bool   `json:"stream"`
}

// RequestMetadata extracts the routing fields without translating content
// blocks. Native Anthropic adapters can therefore accept blocks the fallback
// OpenAI translator does not implement.
func RequestMetadata(data []byte) (string, bool, error) {
	var request requestMetadata
	if err := json.Unmarshal(data, &request); err != nil {
		return "", false, fmt.Errorf("decode Anthropic Messages request: %w", err)
	}
	request.Model = strings.TrimSpace(request.Model)
	if request.Model == "" {
		return "", false, fmt.Errorf("Anthropic model is required")
	}
	return request.Model, request.Stream, nil
}

type messageRequest struct {
	Model       string          `json:"model"`
	System      json.RawMessage `json:"system,omitempty"`
	Messages    []inputMessage  `json:"messages"`
	MaxTokens   int             `json:"max_tokens"`
	Temperature *float64        `json:"temperature,omitempty"`
	TopP        *float64        `json:"top_p,omitempty"`
	Stop        []string        `json:"stop_sequences,omitempty"`
	Stream      bool            `json:"stream,omitempty"`
	Tools       []anthropicTool `json:"tools,omitempty"`
	ToolChoice  json.RawMessage `json:"tool_choice,omitempty"`
}
type inputMessage struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}
type contentBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text,omitempty"`
	ID        string          `json:"id,omitempty"`
	Name      string          `json:"name,omitempty"`
	Input     json.RawMessage `json:"input,omitempty"`
	ToolUseID string          `json:"tool_use_id,omitempty"`
	Content   json.RawMessage `json:"content,omitempty"`
	Source    *imageSource    `json:"source,omitempty"`
}
type imageSource struct {
	Type      string `json:"type"`
	MediaType string `json:"media_type,omitempty"`
	Data      string `json:"data,omitempty"`
	URL       string `json:"url,omitempty"`
}
type anthropicTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"input_schema"`
}

// ToOpenAI converts an Anthropic Messages request into OpenAI Chat Completions.
func ToOpenAI(input []byte) ([]byte, string, bool, error) {
	var in messageRequest
	if err := json.Unmarshal(input, &in); err != nil {
		return nil, "", false, fmt.Errorf("invalid Anthropic request: %w", err)
	}
	if strings.TrimSpace(in.Model) == "" || in.MaxTokens <= 0 || len(in.Messages) == 0 {
		return nil, "", false, fmt.Errorf("model, positive max_tokens and messages are required")
	}
	messages := make([]map[string]any, 0, len(in.Messages)+1)
	system, err := textContent(in.System)
	if err != nil {
		return nil, "", false, err
	}
	if system != "" {
		messages = append(messages, map[string]any{"role": "system", "content": system})
	}
	for _, m := range in.Messages {
		if m.Role != "user" && m.Role != "assistant" {
			return nil, "", false, fmt.Errorf("unsupported message role %q", m.Role)
		}
		blocks, err := decodeBlocks(m.Content)
		if err != nil {
			return nil, "", false, err
		}
		var contentParts []map[string]any
		var textParts []string
		var calls []map[string]any
		var toolMessages []map[string]any
		for _, b := range blocks {
			switch b.Type {
			case "text":
				textParts = append(textParts, b.Text)
				contentParts = append(contentParts, map[string]any{"type": "text", "text": b.Text})
			case "image":
				if m.Role != "user" || b.Source == nil {
					return nil, "", false, fmt.Errorf("image blocks require user role and source")
				}
				var imageURL string
				switch b.Source.Type {
				case "url":
					if strings.TrimSpace(b.Source.URL) == "" {
						return nil, "", false, fmt.Errorf("image URL is required")
					}
					imageURL = b.Source.URL
				case "base64":
					if !strings.HasPrefix(strings.ToLower(b.Source.MediaType), "image/") || b.Source.Data == "" {
						return nil, "", false, fmt.Errorf("base64 image requires an image media_type and data")
					}
					imageURL = "data:" + b.Source.MediaType + ";base64," + b.Source.Data
				default:
					return nil, "", false, fmt.Errorf("unsupported image source type %q", b.Source.Type)
				}
				contentParts = append(contentParts, map[string]any{"type": "image_url", "image_url": map[string]any{"url": imageURL}})
			case "tool_use":
				if m.Role != "assistant" || b.ID == "" || b.Name == "" {
					return nil, "", false, fmt.Errorf("tool_use requires assistant role, id and name")
				}
				args := b.Input
				if len(args) == 0 {
					args = []byte(`{}`)
				}
				if !json.Valid(args) {
					return nil, "", false, fmt.Errorf("tool_use input must be valid JSON")
				}
				calls = append(calls, map[string]any{"id": b.ID, "type": "function", "function": map[string]any{"name": b.Name, "arguments": string(args)}})
			case "tool_result":
				if m.Role != "user" || b.ToolUseID == "" {
					return nil, "", false, fmt.Errorf("tool_result requires user role and tool_use_id")
				}
				content, err := toolResultText(b.Content)
				if err != nil {
					return nil, "", false, err
				}
				toolMessages = append(toolMessages, map[string]any{"role": "tool", "tool_call_id": b.ToolUseID, "content": content})
			default:
				return nil, "", false, fmt.Errorf("unsupported content block type %q", b.Type)
			}
		}
		msg := map[string]any{"role": m.Role, "content": strings.Join(textParts, "")}
		if len(contentParts) > 0 && len(contentParts) != len(textParts) {
			msg["content"] = contentParts
		}
		if len(calls) > 0 {
			msg["tool_calls"] = calls
			if len(contentParts) == 0 {
				msg["content"] = nil
			}
		}
		if len(contentParts) > 0 || len(calls) > 0 {
			messages = append(messages, msg)
		}
		messages = append(messages, toolMessages...)
	}
	out := map[string]any{"model": in.Model, "messages": messages, "max_tokens": in.MaxTokens, "stream": in.Stream}
	if in.Temperature != nil {
		out["temperature"] = *in.Temperature
	}
	if in.TopP != nil {
		out["top_p"] = *in.TopP
	}
	if len(in.Stop) > 0 {
		out["stop"] = in.Stop
	}
	if len(in.Tools) > 0 {
		tools := make([]map[string]any, 0, len(in.Tools))
		for _, t := range in.Tools {
			if t.Name == "" || !json.Valid(t.InputSchema) {
				return nil, "", false, fmt.Errorf("each tool requires name and valid input_schema")
			}
			var schema any
			_ = json.Unmarshal(t.InputSchema, &schema)
			tools = append(tools, map[string]any{"type": "function", "function": map[string]any{"name": t.Name, "description": t.Description, "parameters": schema}})
		}
		out["tools"] = tools
	}
	if len(in.ToolChoice) > 0 {
		choice, err := anthropicToolChoice(in.ToolChoice)
		if err != nil {
			return nil, "", false, err
		}
		out["tool_choice"] = choice
	}
	b, err := json.Marshal(out)
	return b, in.Model, in.Stream, err
}

func FromOpenAI(input []byte, model string) ([]byte, error) {
	var in struct {
		ID      string `json:"id"`
		Choices []struct {
			Message struct {
				Content   any `json:"content"`
				ToolCalls []struct {
					ID       string `json:"id"`
					Function struct {
						Name      string `json:"name"`
						Arguments string `json:"arguments"`
					} `json:"function"`
				} `json:"tool_calls"`
			} `json:"message"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
		Usage struct {
			Prompt     int `json:"prompt_tokens"`
			Completion int `json:"completion_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(input, &in); err != nil {
		return nil, fmt.Errorf("invalid upstream chat completion: %w", err)
	}
	blocks := make([]any, 0)
	stop := "end_turn"
	if len(in.Choices) > 0 {
		choice := in.Choices[0]
		if s, ok := choice.Message.Content.(string); ok && s != "" {
			blocks = append(blocks, map[string]any{"type": "text", "text": s})
		}
		for _, call := range choice.Message.ToolCalls {
			var args any
			if call.Function.Arguments == "" {
				args = map[string]any{}
			} else if err := json.Unmarshal([]byte(call.Function.Arguments), &args); err != nil {
				return nil, fmt.Errorf("invalid tool call arguments: %w", err)
			}
			blocks = append(blocks, map[string]any{"type": "tool_use", "id": call.ID, "name": call.Function.Name, "input": args})
		}
		switch choice.FinishReason {
		case "length":
			stop = "max_tokens"
		case "tool_calls":
			stop = "tool_use"
		}
	}
	out := map[string]any{"id": in.ID, "type": "message", "role": "assistant", "model": model, "content": blocks, "stop_reason": stop, "stop_sequence": nil, "usage": map[string]int{"input_tokens": in.Usage.Prompt, "output_tokens": in.Usage.Completion}}
	return json.Marshal(out)
}

func decodeBlocks(raw json.RawMessage) ([]contentBlock, error) {
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return []contentBlock{{Type: "text", Text: text}}, nil
	}
	var blocks []contentBlock
	if err := json.Unmarshal(raw, &blocks); err != nil {
		return nil, fmt.Errorf("content must be text or a block array")
	}
	return blocks, nil
}
func textContent(raw json.RawMessage) (string, error) {
	if len(raw) == 0 {
		return "", nil
	}
	var value string
	if json.Unmarshal(raw, &value) == nil {
		return value, nil
	}
	blocks, err := decodeBlocks(raw)
	if err != nil {
		return "", fmt.Errorf("system must be text or text blocks")
	}
	var out []string
	for _, block := range blocks {
		if block.Type != "text" {
			return "", fmt.Errorf("unsupported system block type %q", block.Type)
		}
		out = append(out, block.Text)
	}
	return strings.Join(out, ""), nil
}

func toolResultText(raw json.RawMessage) (string, error) {
	if len(raw) == 0 {
		return "", nil
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s, nil
	}
	blocks, err := decodeBlocks(raw)
	if err != nil {
		return "", err
	}
	var out []string
	for _, b := range blocks {
		if b.Type != "text" {
			return "", fmt.Errorf("unsupported tool_result block %q", b.Type)
		}
		out = append(out, b.Text)
	}
	return strings.Join(out, ""), nil
}
func anthropicToolChoice(raw json.RawMessage) (any, error) {
	var c struct {
		Type string `json:"type"`
		Name string `json:"name"`
	}
	if err := json.Unmarshal(raw, &c); err != nil {
		return nil, fmt.Errorf("invalid tool_choice")
	}
	switch c.Type {
	case "auto":
		return "auto", nil
	case "any":
		return "required", nil
	case "tool":
		if c.Name == "" {
			return nil, fmt.Errorf("tool_choice tool requires name")
		}
		return map[string]any{"type": "function", "function": map[string]string{"name": c.Name}}, nil
	default:
		return nil, fmt.Errorf("unsupported tool_choice type %q", c.Type)
	}
}
