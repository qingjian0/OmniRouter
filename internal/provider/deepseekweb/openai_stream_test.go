package deepseekweb

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestOpenAIStreamEncoderMapsContentAndReasoning(t *testing.T) {
	encoder, err := NewOpenAIStreamEncoder("deepseek-chat")
	if err != nil {
		t.Fatal(err)
	}
	payload, err := encoder.Encode(PatchUpdate{
		ContentDelta:   "answer",
		ReasoningDelta: "reasoning",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(payload), "data: ") {
		t.Fatalf("payload is not an SSE data field: %q", payload)
	}
	var chunk struct {
		ID      string `json:"id"`
		Object  string `json:"object"`
		Model   string `json:"model"`
		Choices []struct {
			Delta        map[string]string `json:"delta"`
			FinishReason *string           `json:"finish_reason"`
		} `json:"choices"`
	}
	if err := json.Unmarshal([]byte(strings.TrimPrefix(string(payload), "data: ")), &chunk); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(chunk.ID, "chatcmpl-") || chunk.Object != "chat.completion.chunk" || chunk.Model != "deepseek-chat" {
		t.Fatalf("unexpected chunk envelope: %+v", chunk)
	}
	if len(chunk.Choices) != 1 || chunk.Choices[0].Delta["role"] != "assistant" || chunk.Choices[0].Delta["content"] != "answer" || chunk.Choices[0].Delta["reasoning_content"] != "reasoning" || chunk.Choices[0].FinishReason != nil {
		t.Fatalf("unexpected delta: %+v", chunk.Choices)
	}
}

func TestOpenAIStreamEncoderUsesExplicitFinishChunk(t *testing.T) {
	encoder, err := NewOpenAIStreamEncoder("deepseek-chat")
	if err != nil {
		t.Fatal(err)
	}
	payload, err := encoder.Encode(PatchUpdate{Done: true})
	if err != nil {
		t.Fatal(err)
	}
	var chunk struct {
		Choices []struct {
			FinishReason *string `json:"finish_reason"`
		} `json:"choices"`
	}
	if err := json.Unmarshal([]byte(strings.TrimPrefix(string(payload), "data: ")), &chunk); err != nil {
		t.Fatal(err)
	}
	if len(chunk.Choices) != 1 || chunk.Choices[0].FinishReason == nil || *chunk.Choices[0].FinishReason != "stop" {
		t.Fatalf("missing finish_reason=stop: %+v", chunk)
	}
	if _, err := encoder.Encode(PatchUpdate{ContentDelta: "late"}); err == nil {
		t.Fatal("expected writes after finish to fail")
	}
}

func TestOpenAIStreamEncoderRejectsUnmappedSearchMetadata(t *testing.T) {
	encoder, err := NewOpenAIStreamEncoder("deepseek-chat")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := encoder.Encode(PatchUpdate{SearchDelta: "[{\"title\":\"source\"}]"}); !errors.Is(err, ErrUnsupportedOpenAIStreamUpdate) {
		t.Fatalf("expected unsupported metadata error, got %v", err)
	}
}

func TestOpenAIStreamEncoderOmitsEmptyNonterminalUpdates(t *testing.T) {
	encoder, err := NewOpenAIStreamEncoder("deepseek-chat")
	if err != nil {
		t.Fatal(err)
	}
	payload, err := encoder.Encode(PatchUpdate{Title: "conversation title", ResponseMessageID: "response-1"})
	if err != nil {
		t.Fatal(err)
	}
	if payload != nil {
		t.Fatalf("metadata-only update should not produce a chat chunk: %q", payload)
	}
}
