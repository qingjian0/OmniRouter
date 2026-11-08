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

func TestMapChatCompletionInputReasonerAlias(t *testing.T) {
	got, err := MapChatCompletionInput([]byte(`{"model":"deepseek-reasoner","messages":[{"role":"user","content":"solve"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if got.ModelType != "expert" || !got.ThinkingEnabled {
		t.Fatalf("reasoner alias not mapped: %+v", got)
	}
}

func TestMapChatCompletionInputExplicitOptionsOverrideDefaults(t *testing.T) {
	got, err := MapChatCompletionInput([]byte(`{"model":"deepseek-chat","thinking_enabled":true,"search_enabled":true,"messages":[{"role":"user","content":"search this"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if !got.ThinkingEnabled || !got.SearchEnabled {
		t.Fatalf("explicit options not preserved: %+v", got)
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

func TestMapChatCompletionInputAllowsEmptyToolCalls(t *testing.T) {
	_, err := MapChatCompletionInput([]byte(`{"model":"deepseek-chat","messages":[{"role":"assistant","content":"hello","tool_calls":[]}]}`))
	if err != nil {
		t.Fatalf("empty tool_calls should not change a text-only message: %v", err)
	}
}

func TestMapChatCompletionInputRejectsUnsupportedContentAndTools(t *testing.T) {
	cases := []string{
		`{"model":"deepseek-chat","messages":[{"role":"user","content":[{"type":"text","text":"hello"}]}]}`,
		`{"model":"deepseek-chat","messages":[{"role":"assistant","content":"","tool_calls":[{"id":"call-1"}]}]}`,
		`{"model":"deepseek-chat","messages":[{"role":"tool","content":"result"}]}`,
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
