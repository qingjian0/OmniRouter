// Package gemini translates Gemini generateContent to and from OpenAI Chat
// Completions, including the non-streaming function-calling subset.
package gemini

import (
	"encoding/json"
	"fmt"
	"strings"
)

type part struct {
	Text             string          `json:"text,omitempty"`
	InlineData       *inlineData     `json:"inlineData,omitempty"`
	FileData         *fileData       `json:"fileData,omitempty"`
	FunctionCall     *functionCall   `json:"functionCall,omitempty"`
	FunctionResponse *functionResult `json:"functionResponse,omitempty"`
}
type inlineData struct {
	MIMEType string `json:"mimeType"`
	Data     string `json:"data"`
}
type fileData struct {
	MIMEType string `json:"mimeType,omitempty"`
	FileURI  string `json:"fileUri"`
}
type functionCall struct {
	Name string          `json:"name"`
	Args json.RawMessage `json:"args"`
}
type functionResult struct {
	Name     string          `json:"name"`
	Response json.RawMessage `json:"response"`
}
type functionDeclaration struct {
	Name                 string          `json:"name"`
	Description          string          `json:"description,omitempty"`
	Parameters           json.RawMessage `json:"parameters,omitempty"`
	ParametersJSONSchema json.RawMessage `json:"parametersJsonSchema,omitempty"`
}
type request struct {
	Contents []struct {
		Role  string `json:"role"`
		Parts []part `json:"parts"`
	} `json:"contents"`
	SystemInstruction *struct {
		Parts []part `json:"parts"`
	} `json:"systemInstruction,omitempty"`
	Tools []struct {
		FunctionDeclarations []functionDeclaration `json:"functionDeclarations"`
	} `json:"tools,omitempty"`
	ToolConfig *struct {
		FunctionCallingConfig struct {
			Mode                 string   `json:"mode"`
			AllowedFunctionNames []string `json:"allowedFunctionNames"`
		} `json:"functionCallingConfig"`
	} `json:"toolConfig,omitempty"`
	GenerationConfig struct {
		MaxOutputTokens int      `json:"maxOutputTokens,omitempty"`
		Temperature     *float64 `json:"temperature,omitempty"`
		TopP            *float64 `json:"topP,omitempty"`
		StopSequences   []string `json:"stopSequences,omitempty"`
	} `json:"generationConfig,omitempty"`
}

func ToOpenAI(input []byte, model string) ([]byte, error) {
	return ToOpenAIStream(input, model, false)
}

func ToOpenAIStream(input []byte, model string, stream bool) ([]byte, error) {
	var in request
	if err := json.Unmarshal(input, &in); err != nil {
		return nil, fmt.Errorf("invalid Gemini request: %w", err)
	}
	if strings.TrimSpace(model) == "" || len(in.Contents) == 0 {
		return nil, fmt.Errorf("model and contents are required")
	}
	messages := make([]map[string]any, 0, len(in.Contents)+1)
	// Gemini functionResponse carries the function name but no call ID. Keep
	// an ordered queue per function name so repeated calls to the same function
	// remain correlated with their corresponding OpenAI tool_call IDs.
	pendingCalls := make(map[string][]string)
	callSequence := 0
	if in.SystemInstruction != nil {
		text, err := partsText(in.SystemInstruction.Parts)
		if err != nil {
			return nil, err
		}
		if text != "" {
			messages = append(messages, map[string]any{"role": "system", "content": text})
		}
	}
	for _, content := range in.Contents {
		role := content.Role
		if role == "model" {
			role = "assistant"
		}
		if role != "user" && role != "assistant" {
			return nil, fmt.Errorf("unsupported Gemini role %q", content.Role)
		}
		var texts []string
		var contentParts []map[string]any
		var calls []map[string]any
		var toolMessages []map[string]any
		for _, item := range content.Parts {
			if item.Text != "" {
				texts = append(texts, item.Text)
				contentParts = append(contentParts, map[string]any{"type": "text", "text": item.Text})
			}
			if item.InlineData != nil {
				if role != "user" || !strings.HasPrefix(strings.ToLower(item.InlineData.MIMEType), "image/") || item.InlineData.Data == "" {
					return nil, fmt.Errorf("inlineData currently supports user image parts with mimeType and data")
				}
				url := "data:" + item.InlineData.MIMEType + ";base64," + item.InlineData.Data
				contentParts = append(contentParts, map[string]any{"type": "image_url", "image_url": map[string]any{"url": url}})
			}
			if item.FileData != nil {
				if role != "user" || !strings.HasPrefix(strings.ToLower(item.FileData.MIMEType), "image/") || !(strings.HasPrefix(item.FileData.FileURI, "https://") || strings.HasPrefix(item.FileData.FileURI, "http://")) {
					return nil, fmt.Errorf("fileData currently requires a user image with an HTTP(S) fileUri")
				}
				contentParts = append(contentParts, map[string]any{"type": "image_url", "image_url": map[string]any{"url": item.FileData.FileURI}})
			}
			if item.FunctionCall != nil {
				if role != "assistant" || item.FunctionCall.Name == "" {
					return nil, fmt.Errorf("functionCall requires model role and name")
				}
				args := item.FunctionCall.Args
				if len(args) == 0 {
					args = []byte(`{}`)
				}
				if !isJSONObject(args) {
					return nil, fmt.Errorf("functionCall args must be a JSON object")
				}
				callSequence++
				callID := fmt.Sprintf("gemini_call_%d", callSequence)
				pendingCalls[item.FunctionCall.Name] = append(pendingCalls[item.FunctionCall.Name], callID)
				calls = append(calls, map[string]any{"id": callID, "type": "function", "function": map[string]any{"name": item.FunctionCall.Name, "arguments": string(args)}})
			}
			if item.FunctionResponse != nil {
				if role != "user" || item.FunctionResponse.Name == "" {
					return nil, fmt.Errorf("functionResponse requires user role and name")
				}
				var response any
				if len(item.FunctionResponse.Response) == 0 {
					response = map[string]any{}
				} else if err := json.Unmarshal(item.FunctionResponse.Response, &response); err != nil {
					return nil, fmt.Errorf("functionResponse response must be valid JSON")
				}
				queued := pendingCalls[item.FunctionResponse.Name]
				if len(queued) == 0 {
					return nil, fmt.Errorf("functionResponse %q has no preceding functionCall", item.FunctionResponse.Name)
				}
				callID := queued[0]
				if len(queued) == 1 {
					delete(pendingCalls, item.FunctionResponse.Name)
				} else {
					pendingCalls[item.FunctionResponse.Name] = queued[1:]
				}
				encoded, _ := json.Marshal(response)
				toolMessages = append(toolMessages, map[string]any{"role": "tool", "tool_call_id": callID, "content": string(encoded)})
			}
		}
		message := map[string]any{"role": role, "content": strings.Join(texts, "")}
		if len(contentParts) > 0 && len(contentParts) != len(texts) {
			message["content"] = contentParts
		}
		if len(calls) > 0 {
			message["tool_calls"] = calls
			if len(contentParts) == 0 {
				message["content"] = nil
			}
		}
		if len(contentParts) > 0 || len(calls) > 0 {
			messages = append(messages, message)
		}
		messages = append(messages, toolMessages...)
	}
	out := map[string]any{"model": model, "messages": messages, "stream": stream}
	if in.GenerationConfig.MaxOutputTokens > 0 {
		out["max_tokens"] = in.GenerationConfig.MaxOutputTokens
	}
	if in.GenerationConfig.Temperature != nil {
		out["temperature"] = *in.GenerationConfig.Temperature
	}
	if in.GenerationConfig.TopP != nil {
		out["top_p"] = *in.GenerationConfig.TopP
	}
	if len(in.GenerationConfig.StopSequences) > 0 {
		out["stop"] = in.GenerationConfig.StopSequences
	}
	var tools []map[string]any
	for _, group := range in.Tools {
		for _, declaration := range group.FunctionDeclarations {
			schema := declaration.Parameters
			if len(schema) == 0 {
				schema = declaration.ParametersJSONSchema
			}
			if declaration.Name == "" || !isJSONObject(schema) {
				return nil, fmt.Errorf("function declaration requires a name and parameters JSON object")
			}
			var parameters any
			_ = json.Unmarshal(schema, &parameters)
			tools = append(tools, map[string]any{"type": "function", "function": map[string]any{"name": declaration.Name, "description": declaration.Description, "parameters": parameters}})
		}
	}
	if len(tools) > 0 {
		out["tools"] = tools
	}
	if in.ToolConfig != nil {
		config := in.ToolConfig.FunctionCallingConfig
		switch strings.ToUpper(config.Mode) {
		case "AUTO":
			out["tool_choice"] = "auto"
		case "ANY":
			out["tool_choice"] = "required"
		case "NONE":
			out["tool_choice"] = "none"
		case "":
		default:
			return nil, fmt.Errorf("unsupported function calling mode %q", config.Mode)
		}
		if len(config.AllowedFunctionNames) == 1 {
			out["tool_choice"] = map[string]any{"type": "function", "function": map[string]string{"name": config.AllowedFunctionNames[0]}}
		} else if len(config.AllowedFunctionNames) > 1 {
			return nil, fmt.Errorf("multiple allowedFunctionNames are not representable in the OpenAI tool_choice subset")
		}
	}
	return json.Marshal(out)
}

func FromOpenAI(input []byte) ([]byte, error) {
	var in struct {
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
		return nil, fmt.Errorf("invalid upstream response: %w", err)
	}
	parts := make([]any, 0)
	finish := "STOP"
	if len(in.Choices) > 0 {
		choice := in.Choices[0]
		if text, ok := choice.Message.Content.(string); ok && text != "" {
			parts = append(parts, map[string]any{"text": text})
		}
		for _, call := range choice.Message.ToolCalls {
			var args any
			if call.Function.Arguments == "" {
				args = map[string]any{}
			} else if err := json.Unmarshal([]byte(call.Function.Arguments), &args); err != nil {
				return nil, fmt.Errorf("invalid function call arguments: %w", err)
			}
			parts = append(parts, map[string]any{"functionCall": map[string]any{"name": call.Function.Name, "args": args}})
		}
		if choice.FinishReason == "length" {
			finish = "MAX_TOKENS"
		}
	}
	out := map[string]any{"candidates": []any{map[string]any{"content": map[string]any{"role": "model", "parts": parts}, "finishReason": finish, "index": 0}}, "usageMetadata": map[string]int{"promptTokenCount": in.Usage.Prompt, "candidatesTokenCount": in.Usage.Completion, "totalTokenCount": in.Usage.Prompt + in.Usage.Completion}}
	return json.Marshal(out)
}

func partsText(parts []part) (string, error) {
	if len(parts) == 0 {
		return "", fmt.Errorf("at least one system text part is required")
	}
	var out []string
	for _, item := range parts {
		if item.FunctionCall != nil || item.FunctionResponse != nil || item.InlineData != nil || item.FileData != nil {
			return "", fmt.Errorf("non-text parts are not supported in systemInstruction")
		}
		if item.Text != "" {
			out = append(out, item.Text)
		}
	}
	return strings.Join(out, ""), nil
}

func isJSONObject(raw []byte) bool {
	var value map[string]json.RawMessage
	return len(raw) > 0 && json.Unmarshal(raw, &value) == nil && value != nil
}
