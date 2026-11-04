package anthropic

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// StreamTranslator converts OpenAI Chat Completions SSE data payloads into
// Anthropic Messages events. Tool-call argument fragments are forwarded as
// partial_json deltas while preserving each upstream tool-call index.
type StreamTranslator struct {
	model, id                 string
	inputTokens, outputTokens int
	started                   bool
	stopped                   bool
	textStarted               bool
	textBlock                 int
	nextBlock                 int
	tools                     map[int]*streamTool
	toolOrder                 []int
}

type streamTool struct {
	id, name string
	args     string
	started  bool
	block    int
}

func NewStreamTranslator(model string) *StreamTranslator {
	return &StreamTranslator{model: model, tools: make(map[int]*streamTool)}
}

func (t *StreamTranslator) Process(data []byte) ([][]byte, error) {
	payload := strings.TrimSpace(string(data))
	if payload == "" || payload == "[DONE]" {
		return nil, nil
	}
	var chunk struct {
		ID      string `json:"id"`
		Choices []struct {
			Delta struct {
				Content   any `json:"content"`
				ToolCalls []struct {
					Index    int    `json:"index"`
					ID       string `json:"id"`
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
	if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
		return nil, fmt.Errorf("invalid OpenAI stream chunk: %w", err)
	}
	if chunk.Usage != nil {
		t.inputTokens, t.outputTokens = chunk.Usage.Prompt, chunk.Usage.Completion
	}
	out := make([][]byte, 0, 8)
	if !t.started {
		t.started = true
		t.id = "msg_" + strings.TrimPrefix(chunk.ID, "chatcmpl-")
		if t.id == "msg_" {
			t.id = "msg_stream"
		}
		start, _ := json.Marshal(map[string]any{"type": "message_start", "message": map[string]any{"id": t.id, "type": "message", "role": "assistant", "model": t.model, "content": []any{}, "stop_reason": nil, "stop_sequence": nil, "usage": map[string]int{"input_tokens": t.inputTokens, "output_tokens": t.outputTokens}}})
		out = append(out, frame("message_start", start))
	}
	if len(chunk.Choices) > 0 {
		delta := chunk.Choices[0].Delta
		if text, ok := delta.Content.(string); ok && text != "" {
			if !t.textStarted {
				t.textStarted = true
				t.textBlock = t.nextBlock
				t.nextBlock++
				block, _ := json.Marshal(map[string]any{"type": "content_block_start", "index": t.textBlock, "content_block": map[string]string{"type": "text", "text": ""}})
				out = append(out, frame("content_block_start", block))
			}
			part, _ := json.Marshal(map[string]any{"type": "content_block_delta", "index": t.textBlock, "delta": map[string]string{"type": "text_delta", "text": text}})
			out = append(out, frame("content_block_delta", part))
		}
		for _, call := range delta.ToolCalls {
			state := t.tools[call.Index]
			if state == nil {
				state = &streamTool{block: -1}
				t.tools[call.Index] = state
				t.toolOrder = append(t.toolOrder, call.Index)
			}
			if call.ID != "" {
				state.id = call.ID
			}
			if call.Function.Name != "" {
				state.name += call.Function.Name
			}
			if !state.started && state.id != "" && state.name != "" {
				state.started = true
				state.block = t.nextBlock
				t.nextBlock++
				start, _ := json.Marshal(map[string]any{"type": "content_block_start", "index": state.block, "content_block": map[string]any{"type": "tool_use", "id": state.id, "name": state.name, "input": map[string]any{}}})
				out = append(out, frame("content_block_start", start))
				if state.args != "" {
					part, _ := json.Marshal(map[string]any{"type": "content_block_delta", "index": state.block, "delta": map[string]string{"type": "input_json_delta", "partial_json": state.args}})
					out = append(out, frame("content_block_delta", part))
				}
			}
			if !state.started {
				state.args += call.Function.Arguments
			} else if call.Function.Arguments != "" {
				state.args += call.Function.Arguments
				part, _ := json.Marshal(map[string]any{"type": "content_block_delta", "index": state.block, "delta": map[string]string{"type": "input_json_delta", "partial_json": call.Function.Arguments}})
				out = append(out, frame("content_block_delta", part))
			}
		}
		if chunk.Choices[0].FinishReason != nil && !t.stopped {
			reason := "end_turn"
			switch *chunk.Choices[0].FinishReason {
			case "length":
				reason = "max_tokens"
			case "tool_calls":
				reason = "tool_use"
			}
			out = append(out, t.finish(reason)...)
		}
	}
	return out, nil
}

func (t *StreamTranslator) Finish() [][]byte {
	return t.finish("end_turn")
}

func (t *StreamTranslator) finish(reason string) [][]byte {
	if !t.started || t.stopped {
		return nil
	}
	t.stopped = true
	out := make([][]byte, 0, len(t.tools)+3)
	if t.textStarted {
		block, _ := json.Marshal(map[string]any{"type": "content_block_stop", "index": t.textBlock})
		out = append(out, frame("content_block_stop", block))
	}
	indices := append([]int(nil), t.toolOrder...)
	sort.Ints(indices)
	for _, index := range indices {
		state := t.tools[index]
		if state == nil || !state.started {
			continue
		}
		block, _ := json.Marshal(map[string]any{"type": "content_block_stop", "index": state.block})
		out = append(out, frame("content_block_stop", block))
	}
	delta, _ := json.Marshal(map[string]any{"type": "message_delta", "delta": map[string]any{"stop_reason": reason, "stop_sequence": nil}, "usage": map[string]int{"input_tokens": t.inputTokens, "output_tokens": t.outputTokens}})
	out = append(out, frame("message_delta", delta), frame("message_stop", []byte(`{"type":"message_stop"}`)))
	return out
}

func frame(event string, data []byte) []byte {
	return []byte("event: " + event + "\ndata: " + string(data) + "\n\n")
}
