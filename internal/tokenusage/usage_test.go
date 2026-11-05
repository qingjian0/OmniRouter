package tokenusage

import "testing"

func TestParseOpenAIUsageDistinguishesMissingAndZero(t *testing.T) {
	got, err := ParseOpenAI([]byte(`{"usage":{"prompt_tokens":0,"completion_tokens":7,"total_tokens":7}}`), true)
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || got.InputTokens == nil || *got.InputTokens != 0 || got.OutputTokens == nil || *got.OutputTokens != 7 || got.TotalTokens == nil || *got.TotalTokens != 7 {
		t.Fatalf("unexpected usage: %#v", got)
	}
	missing, err := ParseOpenAI([]byte(`{"choices":[]}`), true)
	if err != nil {
		t.Fatal(err)
	}
	if missing != nil {
		t.Fatalf("expected missing usage, got %#v", missing)
	}
}

func TestParseOpenAIRejectsInvalidJSON(t *testing.T) {
	if _, err := ParseOpenAI([]byte(`{`), false); err == nil {
		t.Fatal("expected JSON error")
	}
}

func TestParseAnthropicUsage(t *testing.T) {
	got, err := ParseAnthropic([]byte(`{"usage":{"input_tokens":11,"output_tokens":13}}`), true)
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || *got.InputTokens != 11 || *got.OutputTokens != 13 || got.TotalTokens == nil || *got.TotalTokens != 24 {
		t.Fatalf("unexpected usage: %#v", got)
	}
}

func TestParseGeminiUsage(t *testing.T) {
	got, err := ParseGemini([]byte(`{"usageMetadata":{"promptTokenCount":5,"candidatesTokenCount":8,"totalTokenCount":13}}`), false)
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || *got.InputTokens != 5 || *got.OutputTokens != 8 || got.Final {
		t.Fatalf("unexpected usage: %#v", got)
	}
}

func TestParseAnthropicStreamMessageStartUsage(t *testing.T) {
	got, err := ParseAnthropic([]byte(`{"type":"message_start","message":{"usage":{"input_tokens":9}}}`), false)
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || got.InputTokens == nil || *got.InputTokens != 9 || got.OutputTokens != nil {
		t.Fatalf("unexpected usage: %#v", got)
	}
}

func TestParsersRejectNegativeProviderTokenCounts(t *testing.T) {
	tests := []struct {
		name  string
		parse func([]byte, bool) (*Usage, error)
		data  []byte
	}{
		{
			name:  "openai input",
			parse: ParseOpenAI,
			data:  []byte(`{"usage":{"prompt_tokens":-1,"completion_tokens":2,"total_tokens":1}}`),
		},
		{
			name:  "openai total",
			parse: ParseOpenAI,
			data:  []byte(`{"usage":{"prompt_tokens":1,"completion_tokens":2,"total_tokens":-3}}`),
		},
		{
			name:  "anthropic output",
			parse: ParseAnthropic,
			data:  []byte(`{"usage":{"input_tokens":1,"output_tokens":-2}}`),
		},
		{
			name:  "gemini prompt",
			parse: ParseGemini,
			data:  []byte(`{"usageMetadata":{"promptTokenCount":-1,"candidatesTokenCount":2}}`),
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := test.parse(test.data, true); err == nil {
				t.Fatal("expected negative token count to be rejected")
			}
		})
	}
}

func TestParseAnthropicRejectsInt64TotalOverflow(t *testing.T) {
	data := []byte(`{"usage":{"input_tokens":9223372036854775807,"output_tokens":1}}`)
	if _, err := ParseAnthropic(data, true); err == nil {
		t.Fatal("expected overflowing token total to be rejected")
	}
}

func TestParseResponsesUsageFromResponseAndStreamEvent(t *testing.T) {
	response, err := ParseResponses([]byte(`{"usage":{"input_tokens":12,"output_tokens":8,"total_tokens":20}}`), true)
	if err != nil {
		t.Fatal(err)
	}
	if response == nil || response.InputTokens == nil || *response.InputTokens != 12 || response.OutputTokens == nil || *response.OutputTokens != 8 || response.TotalTokens == nil || *response.TotalTokens != 20 || !response.Final {
		t.Fatalf("unexpected Responses usage: %#v", response)
	}
	stream, err := ParseResponses([]byte(`{"type":"response.completed","response":{"usage":{"input_tokens":3,"output_tokens":4,"total_tokens":7}}}`), true)
	if err != nil {
		t.Fatal(err)
	}
	if stream == nil || stream.InputTokens == nil || *stream.InputTokens != 3 || stream.OutputTokens == nil || *stream.OutputTokens != 4 {
		t.Fatalf("unexpected streamed Responses usage: %#v", stream)
	}
}
