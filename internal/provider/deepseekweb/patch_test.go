package deepseekweb

import (
	"errors"
	"testing"
)

func TestPatchDecoderInitialAndAppend(t *testing.T) {
	decoder := &PatchDecoder{}
	first, err := decoder.Decode([]byte(`{"v":{"response":{"fragments":[{"type":"RESPONSE","content":"hello"}]}}}`))
	if err != nil {
		t.Fatal(err)
	}
	if first.ContentDelta != "hello" || first.Done {
		t.Fatalf("unexpected initial update: %+v", first)
	}
	next, err := decoder.Decode([]byte(`{"v":" world"}`))
	if err != nil {
		t.Fatal(err)
	}
	if next.ContentDelta != " world" {
		t.Fatalf("unexpected append: %+v", next)
	}
}

func TestPatchDecoderTracksThinkingWithoutExposingIt(t *testing.T) {
	decoder := &PatchDecoder{}
	thinking, err := decoder.Decode([]byte(`{"p":"response/fragments","o":"APPEND","v":[{"type":"THINK","content":"private reasoning"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if thinking.ContentDelta != "" {
		t.Fatalf("THINK fragment leaked: %+v", thinking)
	}
	appendThinking, err := decoder.Decode([]byte(`{"p":"response/fragments/-1/content","v":"more private reasoning"}`))
	if err != nil {
		t.Fatal(err)
	}
	if appendThinking.ContentDelta != "" {
		t.Fatalf("THINK append leaked: %+v", appendThinking)
	}
	answer, err := decoder.Decode([]byte(`{"p":"response/fragments","o":"APPEND","v":[{"type":"RESPONSE","content":"public"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if answer.ContentDelta != "public" {
		t.Fatalf("response fragment missing: %+v", answer)
	}
}

func TestPatchDecoderRecognizesExplicitTerminal(t *testing.T) {
	decoder := &PatchDecoder{}
	update, err := decoder.Decode([]byte(`{"p":"response/status","v":"FINISHED"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !update.Done {
		t.Fatalf("terminal status not recognized: %+v", update)
	}
}

func TestPatchDecoderBatchTerminalAndUsage(t *testing.T) {
	decoder := &PatchDecoder{}
	update, err := decoder.Decode([]byte(`{"p":"response","o":"BATCH","v":[{"p":"quasi_status","v":"FINISHED"},{"p":"accumulated_token_usage","v":42}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if !update.Done || update.CompletionTokens != 42 {
		t.Fatalf("unexpected batch update: %+v", update)
	}
}

func TestPatchDecoderFailsClosedOnUnknownPath(t *testing.T) {
	decoder := &PatchDecoder{}
	_, err := decoder.Decode([]byte(`{"p":"response/new_private_field","v":"x"}`))
	if !errors.Is(err, ErrUnsupportedPatch) {
		t.Fatalf("expected ErrUnsupportedPatch, got %v", err)
	}
}

func TestPatchDecoderRejectsMalformedPayload(t *testing.T) {
	decoder := &PatchDecoder{}
	if _, err := decoder.Decode([]byte(`{"p":"response/fragments","v":"not-an-array"}`)); !errors.Is(err, ErrUnsupportedPatch) {
		t.Fatalf("expected unsupported patch, got %v", err)
	}
}
