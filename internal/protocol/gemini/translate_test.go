package gemini

import (
	"encoding/json"
	"strings"
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


func TestImageAndFunctionCallTranslation(t *testing.T) {
	request := []byte(`{"contents":[{"role":"user","parts":[{"text":"Inspect this image"},{"inlineData":{"mimeType":"image/png","data":"aGVsbG8="}}]}],"tools":[{"functionDeclarations":[{"name":"lookup","description":"Look up a record","parameters":{"type":"object","properties":{"id":{"type":"string"}}}}]}]}`)
	translated, err := ToOpenAIStream(request, "gemini-test", false)
	if err != nil {
		t.Fatalf("ToOpenAIStream image/tool request: %v", err)
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
		t.Fatalf("function declaration was not preserved: %s", translated)
	}

	upstream := []byte(`{"choices":[{"message":{"role":"assistant","tool_calls":[{"id":"call_1","type":"function","function":{"name":"lookup","arguments":"{\"id\":\"42\"}"}}]},"finish_reason":"tool_calls"}]}`)
	output, err := FromOpenAI(upstream)
	if err != nil {
		t.Fatalf("FromOpenAI function response: %v", err)
	}
	var response struct {
		Candidates []struct {
			Content struct {
				Parts []struct {
					FunctionCall struct {
						Name string `json:"name"`
					} `json:"functionCall"`
				} `json:"parts"`
			} `json:"content"`
		} `json:"candidates"`
	}
	if err := json.Unmarshal(output, &response); err != nil {
		t.Fatalf("decode Gemini function response: %v", err)
	}
	if len(response.Candidates) != 1 || len(response.Candidates[0].Content.Parts) != 1 || response.Candidates[0].Content.Parts[0].FunctionCall.Name != "lookup" {
		t.Fatalf("unexpected Gemini function response: %s", output)
	}
}
