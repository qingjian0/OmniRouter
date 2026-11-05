package gemini

import (
	"encoding/json"
	"testing"
)

func TestTextRequestAndResponseTranslation(t *testing.T) {
	request := []byte(`{"contents":[{"role":"user","parts":[{"text":"Hello"}]}],"generationConfig":{"maxOutputTokens":64}}`)
	translated, err := ToOpenAIStream(request, "gemini-test", false)
	if err != nil {
		t.Fatalf("ToOpenAIStream: %v", err)
	}
	var openAI struct {
		Model    string `json:"model"`
		Stream   bool   `json:"stream"`
		Messages []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(translated, &openAI); err != nil {
		t.Fatalf("decode OpenAI request: %v", err)
	}
	if openAI.Model != "gemini-test" || openAI.Stream || len(openAI.Messages) != 1 || openAI.Messages[0].Content != "Hello" {
		t.Fatalf("unexpected OpenAI request: %s", translated)
	}

	upstream := []byte(`{"choices":[{"message":{"role":"assistant","content":"Hello back"},"finish_reason":"stop"}],"usage":{"prompt_tokens":4,"completion_tokens":3}}`)
	output, err := FromOpenAI(upstream)
	if err != nil {
		t.Fatalf("FromOpenAI: %v", err)
	}
	var response struct {
		Candidates []struct {
			Content struct {
				Role  string `json:"role"`
				Parts []struct {
					Text string `json:"text"`
				} `json:"parts"`
			} `json:"content"`
		} `json:"candidates"`
	}
	if err := json.Unmarshal(output, &response); err != nil {
		t.Fatalf("decode Gemini response: %v", err)
	}
	if len(response.Candidates) != 1 || response.Candidates[0].Content.Role != "model" || len(response.Candidates[0].Content.Parts) != 1 || response.Candidates[0].Content.Parts[0].Text != "Hello back" {
		t.Fatalf("unexpected Gemini response: %s", output)
	}
}

func TestStreamTranslatorEmitsCandidateChunk(t *testing.T) {
	translator := NewStreamTranslator()
	chunk, err := translator.Process([]byte(`{"choices":[{"index":0,"delta":{"content":"hello"},"finish_reason":"stop"}]}`))
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if len(chunk) == 0 {
		t.Fatal("expected Gemini candidate chunk")
	}
	var response struct {
		Candidates []struct {
			Content struct {
				Parts []struct {
					Text string `json:"text"`
				} `json:"parts"`
			} `json:"content"`
			FinishReason string `json:"finishReason"`
		} `json:"candidates"`
	}
	if err := json.Unmarshal(chunk, &response); err != nil {
		t.Fatalf("decode Gemini stream chunk: %v", err)
	}
	if len(response.Candidates) != 1 || response.Candidates[0].FinishReason != "STOP" || len(response.Candidates[0].Content.Parts) != 1 || response.Candidates[0].Content.Parts[0].Text != "hello" {
		t.Fatalf("unexpected Gemini stream chunk: %s", chunk)
	}
}
