package gemini

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

type streamToolCall struct {
	name string
	args strings.Builder
}

// StreamTranslator converts OpenAI SSE deltas to Gemini candidate chunks.
// Function-call argument fragments are buffered by tool-call index and emitted
// as complete JSON objects when the upstream finishes the assistant turn.
type StreamTranslator struct {
	tools    map[int]*streamToolCall
	order    []int
	finished bool
}

func NewStreamTranslator() *StreamTranslator {
	return &StreamTranslator{tools: make(map[int]*streamToolCall)}
}

func (t *StreamTranslator) Process(data []byte) ([]byte, error) {
	payload := strings.TrimSpace(string(data))
	if payload == "" || payload == "[DONE]" {
		return nil, nil
	}
	var in struct {
		Choices []struct {
			Delta struct {
				Content   any `json:"content"`
				ToolCalls []struct {
					Index    int `json:"index"`
					Function struct {
						Name      string `json:"name"`
						Arguments string `json:"arguments"`
					} `json:"function"`
				} `json:"tool_calls"`
			} `json:"delta"`
			FinishReason *string `json:"finish_reason"`
		} `json:"choices"`
		Usage *struct {
			Prompt     int `json:"prompt_tokens"`
			Completion int `json:"completion_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal([]byte(payload), &in); err != nil {
		return nil, fmt.Errorf("invalid OpenAI stream chunk: %w", err)
	}
	parts := make([]any, 0)
	finish := ""
	if len(in.Choices) > 0 {
		delta := in.Choices[0].Delta
		if text, ok := delta.Content.(string); ok && text != "" {
			parts = append(parts, map[string]string{"text": text})
		}
		for _, call := range delta.ToolCalls {
			state := t.tools[call.Index]
			if state == nil {
				state = &streamToolCall{}
				t.tools[call.Index] = state
				t.order = append(t.order, call.Index)
			}
			state.name += call.Function.Name
			state.args.WriteString(call.Function.Arguments)
		}
		if in.Choices[0].FinishReason != nil {
			finish = "STOP"
			if *in.Choices[0].FinishReason == "length" {
				finish = "MAX_TOKENS"
			}
		}
	}
	if finish != "" {
		toolParts, err := t.finishTools()
		if err != nil {
			return nil, err
		}
		parts = append(parts, toolParts...)
		t.finished = true
	}
	if len(parts) == 0 && finish == "" && in.Usage == nil {
		return nil, nil
	}
	candidate := map[string]any{"content": map[string]any{"role": "model", "parts": parts}, "index": 0}
	if finish != "" {
		candidate["finishReason"] = finish
	}
	out := map[string]any{"candidates": []any{candidate}}
	if in.Usage != nil {
		out["usageMetadata"] = map[string]int{"promptTokenCount": in.Usage.Prompt, "candidatesTokenCount": in.Usage.Completion, "totalTokenCount": in.Usage.Prompt + in.Usage.Completion}
	}
	return json.Marshal(out)
}

// Finish flushes buffered function calls if an upstream ends without a
// finish_reason chunk. It returns nil when no buffered calls remain.
func (t *StreamTranslator) Finish() ([]byte, error) {
	if t.finished || len(t.tools) == 0 {
		return nil, nil
	}
	parts, err := t.finishTools()
	if err != nil {
		return nil, err
	}
	t.finished = true
	return json.Marshal(map[string]any{"candidates": []any{map[string]any{"content": map[string]any{"role": "model", "parts": parts}, "finishReason": "STOP", "index": 0}}})
}

func (t *StreamTranslator) finishTools() ([]any, error) {
	indices := append([]int(nil), t.order...)
	sort.Ints(indices)
	parts := make([]any, 0, len(indices))
	for _, index := range indices {
		call := t.tools[index]
		if call == nil || call.name == "" {
			return nil, fmt.Errorf("streamed function call at index %d is missing its name", index)
		}
		args := strings.TrimSpace(call.args.String())
		if args == "" {
			args = "{}"
		}
		var value map[string]any
		if err := json.Unmarshal([]byte(args), &value); err != nil || value == nil {
			return nil, fmt.Errorf("streamed function call %q has invalid JSON object arguments", call.name)
		}
		parts = append(parts, map[string]any{"functionCall": map[string]any{"name": call.name, "args": value}})
	}
	return parts, nil
}

// StreamFromOpenAI is a stateless convenience wrapper. Callers handling a
// multi-event stream should use StreamTranslator to preserve tool-call deltas.
func StreamFromOpenAI(data []byte) ([]byte, error) {
	return NewStreamTranslator().Process(data)
}
