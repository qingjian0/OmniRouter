package deepseekweb

import (
	"errors"
	"strings"
	"testing"
)

func TestMapChatCompletionInputRoleTaggedPrompt(t *testing.T) {
	got, err := MapChatCompletionInput([]byte(`{"model":"deepseek-chat","messages":[{"role":"system","content":"be concise"},{"role":"user","content":"hello"},{"role":"assistant","content":"hi"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	want := "[System]: be concise\n\n[User]: hello\n\n[Assistant]: hi"
	if got.Prompt != want {
		t.Fatalf("prompt mismatch:\n got: %q\nwant: %q", got.Prompt, want)
	}
	if got.ModelType != "default" || got.ThinkingEnabled || got.SearchEnabled {
		t.Fatalf("unexpected model options: %+v", got)
	}
}

func TestMapChatCompletionInputModelAliases(t *testing.T) {
	cases := []struct {
		model    string
		typeName string
		thinking bool
		search   bool
	}{
		{"deepseek-reasoner", "expert", true, false},
		{"deepseek-r1", "expert", true, false},
		{"deepseek-v3-pro", "expert", false, false},
		{"deepseek-chat-think", "default", true, false},
		{"deepseek-search", "default", false, true},
	}
	for _, tc := range cases {
		got, err := MapChatCompletionInput([]byte(`{"model":"`+tc.model+`","messages":[{"role":"user","content":"hello"}]}`))
		if err != nil {
			t.Fatalf("%s: %v", tc.model, err)
		}
		if got.ModelType != tc.typeName || got.ThinkingEnabled != tc.thinking || got.SearchEnabled != tc.search {
			t.Fatalf("%s: unexpected options: %+v", tc.model, got)
		}
	}
}

func TestMapChatCompletionInputExplicitOptionsOverrideModelDefaults(t *testing.T) {
	got, err := MapChatCompletionInput([]byte(`{"model":"deepseek-reasoner","thinking_enabled":false,"search_enabled":true,"messages":[{"role":"user","content":"search this"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if got.ThinkingEnabled || !got.SearchEnabled {
		t.Fatalf("explicit options not preserved: %+v", got)
	}
}

func TestMapChatCompletionInputOptionAliases(t *testing.T) {
	got, err := MapChatCompletionInput([]byte(`{"model":"deepseek-chat","thinking":true,"web_search":true,"messages":[{"role":"user","content":"search this"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if !got.ThinkingEnabled || !got.SearchEnabled {
		t.Fatalf("option aliases not mapped: %+v", got)
	}
}

func TestMapChatCompletionInputReasoningEffort(t *testing.T) {
	got, err := MapChatCompletionInput([]byte(`{"model":"deepseek-chat","reasoning_effort":"high","messages":[{"role":"user","content":"think"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if !got.ThinkingEnabled {
		t.Fatalf("reasoning_effort did not enable thinking: %+v", got)
	}
}

func TestMapChatCompletionInputPreservesHistoricalToolCallsAndResults(t *testing.T) {
	input := `{"model":"deepseek-chat","messages":[{"role":"system","content":"follow instructions"},{"role":"user","content":"find a file"},{"role":"assistant","content":"I will inspect it.","tool_calls":[{"id":"call-1","type":"function","function":{"name":"read_file","arguments":"{\"path\":\"README.md\"}"}}]},{"role":"tool","tool_call_id":"call-1","content":"file contents"},{"role":"assistant","content":"The file says hello."},{"role":"user","content":"summarize it"}]}`
	got, err := MapChatCompletionInput([]byte(input))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"[System]: follow instructions",
		"[User]: find a file",
		"[Assistant]: I will inspect it.",
		`<tool_call name="read_file">{"path":"README.md"}</tool_call>`,
		"[Tool result for read_file]: file contents",
		"[Assistant]: The file says hello.",
		"[User]: summarize it",
	} {
		if !strings.Contains(got.Prompt, want) {
			t.Fatalf("prompt missing %q: %s", want, got.Prompt)
		}
	}
}

func TestMapChatCompletionInputBoundsConversationHistory(t *testing.T) {
	messages := make([]string, 0, 25)
	for i := 0; i < 24; i++ {
		messages = append(messages, `{"role":"user","content":"turn-`+string(rune('A'+i))+`"}`)
	}
	input := `{"model":"deepseek-chat","messages":[`+strings.Join(messages,",")+ `]}`
	got, err := MapChatCompletionInput([]byte(input))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(got.Prompt, "turn-A") || !strings.Contains(got.Prompt, "turn-X") {
		t.Fatalf("history window was not applied: %s", got.Prompt)
	}
}

func TestMapChatCompletionInputRejectsCurrentToolDefinitionsUntilResponseTranslatorExists(t *testing.T) {
	input := []byte(`{"model":"deepseek-chat","tools":[{"type":"function","function":{"name":"read_file","parameters":{"type":"object"}}}],"messages":[{"role":"user","content":"read"}]}`)
	if _, err := MapChatCompletionInput(input); !errors.Is(err, ErrUnsupportedChatRequest) {
		t.Fatalf("expected tools[] to fail closed, got %v", err)
	}
}

func TestMapChatCompletionInputRejectsUnsupportedContentAndRoles(t *testing.T) {
	cases := []string{
		`{"model":"deepseek-chat","messages":[{"role":"user","content":[{"type":"text","text":"hello"}]}]}`,
		`{"model":"deepseek-chat","messages":[{"role":"function","content":"result"}]}`,
		`{"model":"deepseek-chat","messages":[{"role":"assistant","tool_calls":[{"id":"call-1","function":{"name":"f","arguments":"not-json"}}]}]}`,
	}
	for _, input := range cases {
		if _, err := MapChatCompletionInput([]byte(input)); !errors.Is(err, ErrUnsupportedChatRequest) {
			t.Fatalf("expected unsupported request for %s, got %v", input, err)
		}
	}
}

func TestMapChatCompletionInputRejectsMalformedAndTrailingJSON(t *testing.T) {
	cases := []string{
		`{"model":"deepseek-chat","messages":[`,
		`{"model":"deepseek-chat","messages":[{"role":"user","content":"x"}]} {}`,
		`{"model":"","messages":[{"role":"user","content":"x"}]}`,
	}
	for _, input := range cases {
		if _, err := MapChatCompletionInput([]byte(input)); !errors.Is(err, ErrInvalidChatRequest) {
			t.Fatalf("expected invalid request for %s, got %v", input, err)
		}
	}
}

func TestMapChatCompletionInputPreservesMessageWhitespace(t *testing.T) {
	got, err := MapChatCompletionInput([]byte(`{"model":"deepseek-chat","messages":[{"role":"user","content":"  keep\nspacing  \n"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got.Prompt, "  keep\nspacing  \n") {
		t.Fatalf("message content was normalized: %q", got.Prompt)
	}
}
