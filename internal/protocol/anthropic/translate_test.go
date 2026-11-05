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
