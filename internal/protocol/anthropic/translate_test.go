package anthropic

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestTextRequestAndResponseTranslation(t *testing.T) {
	request := []byte(`{"model":"claude-test","max_tokens":64,"system":"Be concise","messages":[{"role":"user","content":"Hello"}]}`)
	translated, model, stream, err := ToOpenAI(request)
	if err != nil {
		t.Fatalf("ToOpenAI: %v", err)
	}
	if model != "claude-test" || stream {
		t.Fatalf("model/stream = %q/%v", model, stream)
	}
	var openAI struct {
		Model    string `json:"model"`
		Messages []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(translated, &openAI); err != nil {
		t.Fatalf("decode OpenAI request: %v", err)
	}
	if openAI.Model != "claude-test" || len(openAI.Messages) != 2 || openAI.Messages[0].Role != "system" || openAI.Messages[1].Content != "Hello" {
		t.Fatalf("unexpected OpenAI request: %s", translated)
	}

	upstream := []byte(`{"id":"chatcmpl-1","choices":[{"message":{"role":"assistant","content":"Hello back"},"finish_reason":"stop"}],"usage":{"prompt_tokens":3,"completion_tokens":2}}`)
	output, err := FromOpenAI(upstream, model)
	if err != nil {
		t.Fatalf("FromOpenAI: %v", err)
	}
	var message struct {
		Type    string `json:"type"`
		Role    string `json:"role"`
		Model   string `json:"model"`
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(output, &message); err != nil {
		t.Fatalf("decode Anthropic response: %v", err)
	}
	if message.Type != "message" || message.Role != "assistant" || message.Model != model || len(message.Content) != 1 || message.Content[0].Text != "Hello back" {
		t.Fatalf("unexpected Anthropic response: %s", output)
	}
}

func TestStreamTranslatorEmitsTerminalMessageEvents(t *testing.T) {
	translator := NewStreamTranslator("claude-test")
	frames, err := translator.Process([]byte(`{"id":"chatcmpl-stream","choices":[{"index":0,"delta":{"content":"hello"}}]}`))
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if len(frames) == 0 || !strings.Contains(string(frames[0]), "event: message_start") {
		t.Fatalf("missing message_start frame: %q", frames)
	}
	finished := translator.Finish()
	joined := ""
	for _, frame := range finished {
		joined += string(frame)
	}
	if !strings.Contains(joined, "event: message_delta") || !strings.Contains(joined, "event: message_stop") {
		t.Fatalf("missing terminal Anthropic events: %q", joined)
	}
}

func TestImageAndToolTranslation(t *testing.T) {
	request := []byte(`{"model":"claude-test","max_tokens":64,"messages":[{"role":"user","content":[{"type":"text","text":"Inspect this image"},{"type":"image","source":{"type":"base64","media_type":"image/png","data":"aGVsbG8="}}]}],"tools":[{"name":"lookup","description":"Look up a record","input_schema":{"type":"object","properties":{"id":{"type":"string"}}}}]}`)
	translated, _, _, err := ToOpenAI(request)
	if err != nil {
		t.Fatalf("ToOpenAI image/tool request: %v", err)
	}
	var openAI struct {
		Messages []struct {
			Content []struct {
				Type     string `json:"type"`
				ImageURL struct {
					URL string `json:"url"`
				} `json:"image_url"`
			} `json:"content"`
		} `json:"messages"`
		Tools []struct {
			Type string `json:"type"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(translated, &openAI); err != nil {
		t.Fatalf("decode OpenAI request: %v", err)
	}
	if len(openAI.Messages) != 1 || len(openAI.Messages[0].Content) != 2 || openAI.Messages[0].Content[1].Type != "image_url" || !strings.HasPrefix(openAI.Messages[0].Content[1].ImageURL.URL, "data:image/png;base64,") {
		t.Fatalf("image was not preserved in OpenAI request: %s", translated)
	}
	if len(openAI.Tools) != 1 || openAI.Tools[0].Type != "function" {
		t.Fatalf("tool declaration was not preserved: %s", translated)
	}

	upstream := []byte(`{"id":"chatcmpl-tool","choices":[{"message":{"role":"assistant","tool_calls":[{"id":"call_1","type":"function","function":{"name":"lookup","arguments":"{\"id\":\"42\"}"}}]},"finish_reason":"tool_calls"}]}`)
	output, err := FromOpenAI(upstream, "claude-test")
	if err != nil {
		t.Fatalf("FromOpenAI tool response: %v", err)
	}
	var response struct {
		Content []struct {
			Type string `json:"type"`
			Name string `json:"name"`
		} `json:"content"`
		StopReason string `json:"stop_reason"`
	}
	if err := json.Unmarshal(output, &response); err != nil {
		t.Fatalf("decode Anthropic tool response: %v", err)
	}
	if len(response.Content) != 1 || response.Content[0].Type != "tool_use" || response.Content[0].Name != "lookup" || response.StopReason != "tool_use" {
		t.Fatalf("unexpected Anthropic tool response: %s", output)
	}
}
