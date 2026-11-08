package deepseekweb

import (
	"errors"
	"testing"
)

func TestPatchDecoderInitialAndAppend(t *testing.T) {
	decoder := &PatchDecoder{}
	first, err := decoder.Decode([]byte(`{"v":{"response":{"fragments":[{"type":"RESPONSE","content":"hello"}]}}`))
	if err != nil { t.Fatal(err) }
	if first.ContentDelta != "hello" || first.Done { t.Fatalf("unexpected initial update: %+v", first) }
	next, err := decoder.Decode([]byte(`{"v":" world"}`))
	if err != nil { t.Fatal(err) }
	if next.ContentDelta != " world" { t.Fatalf("unexpected append: %+v", next) }
}

func TestPatchDecoderTracksReasoningSeparately(t *testing.T) {
	decoder := &PatchDecoder{}
	thinking, err := decoder.Decode([]byte(`{"p":"response/fragments","o":"APPEND","v":[{"type":"THINK","content":"private reasoning"}]}`))
	if err != nil { t.Fatal(err) }
	if thinking.ContentDelta != "" || thinking.ReasoningDelta != "private reasoning" { t.Fatalf("reasoning was not kept separate: %+v", thinking) }
	appendThinking, err := decoder.Decode([]byte(`{"p":"response/fragments/-1/content","o":"APPEND","v":" more private reasoning"}`))
	if err != nil { t.Fatal(err) }
	if appendThinking.ContentDelta != "" || appendThinking.ReasoningDelta != " more private reasoning" { t.Fatalf("reasoning append was not kept separate: %+v", appendThinking) }
	answer, err := decoder.Decode([]byte(`{"p":"response/fragments","o":"APPEND","v":[{"type":"RESPONSE","content":"public"}]}`))
	if err != nil { t.Fatal(err) }
	if answer.ContentDelta != "public" || answer.ReasoningDelta != "" { t.Fatalf("response fragment missing: %+v", answer) }
}

func TestPatchDecoderSupportsAnswerAliasAndAllFragments(t *testing.T) {
	decoder := &PatchDecoder{}
	update, err := decoder.Decode([]byte(`{"p":"response/fragments","o":"APPEND","v":[{"type":"THINK","content":"thought"},{"type":"ANSWER","content":"answer"}]}`))
	if err != nil { t.Fatal(err) }
	if update.ReasoningDelta != "thought" || update.ContentDelta != "answer" { t.Fatalf("all fragment channels should be preserved: %+v", update) }
}

func TestPatchDecoderRecognizesExplicitTerminal(t *testing.T) {
	decoder := &PatchDecoder{}
	update, err := decoder.Decode([]byte(`{"p":"response/status","v":"FINISHED"}`))
	if err != nil { t.Fatal(err) }
	if !update.Done { t.Fatalf("terminal status not recognized: %+v", update) }
}

func TestPatchDecoderBatchTerminalAndUsage(t *testing.T) {
	decoder := &PatchDecoder{}
	update, err := decoder.Decode([]byte(`{"p":"response","o":"BATCH","v":[{"p":"quasi_status","v":"FINISHED"},{"p":"accumulated_token_usage","v":42}]}`))
	if err != nil { t.Fatal(err) }
	if !update.Done || update.CompletionTokens != 42 { t.Fatalf("unexpected batch update: %+v", update) }
}

func TestPatchDecoderSSEEventLifecycle(t *testing.T) {
	decoder := &PatchDecoder{}
	ready, err := decoder.DecodeEvent("ready", []byte(`{"request_message_id":"req-1","response_message_id":"resp-1"}`))
	if err != nil { t.Fatal(err) }
	if ready.RequestMessageID != "req-1" || ready.ResponseMessageID != "resp-1" { t.Fatalf("ready IDs not decoded: %+v", ready) }
	title, err := decoder.DecodeEvent("title", []byte(`{"content":"A title"}`))
	if err != nil { t.Fatal(err) }
	if title.Title != "A title" { t.Fatalf("title not decoded: %+v", title) }
	closed, err := decoder.DecodeEvent("close", []byte(`{}`))
	if err != nil { t.Fatal(err) }
	if !closed.Done { t.Fatalf("close event must terminate stream: %+v", closed) }
}

func TestPatchDecoderDecodesNumericMessageIDs(t *testing.T) {
	decoder := &PatchDecoder{}
	ready, err := decoder.DecodeEvent("ready", []byte(`{"request_message_id":123,"response_message_id":456}`))
	if err != nil { t.Fatal(err) }
	if ready.RequestMessageID != "123" || ready.ResponseMessageID != "456" { t.Fatalf("numeric IDs not normalized: %+v", ready) }
}

func TestPatchDecoderDoesNotTreatDoneSentinelAsTerminal(t *testing.T) {
	decoder := &PatchDecoder{}
	update, err := decoder.Decode([]byte("[DONE]"))
	if err != nil { t.Fatal(err) }
	if update.Done { t.Fatal("[DONE] is not a verified DeepSeek terminal event") }
}

func TestPatchDecoderIgnoresSearchMetadataWithoutLeakingIt(t *testing.T) {
	decoder := &PatchDecoder{}
	update, err := decoder.Decode([]byte(`{"p":"response/search_results","o":"APPEND","v":[{"title":"example","url":"https://example.test"}]}`))
	if err != nil { t.Fatal(err) }
	if update.ContentDelta != "" || update.ReasoningDelta != "" { t.Fatalf("search metadata leaked into text: %+v", update) }
}

func TestPatchDecoderFailsClosedOnUnknownPath(t *testing.T) {
	decoder := &PatchDecoder{}
	_, err := decoder.Decode([]byte(`{"p":"response/new_private_field","v":"x"}`))
	if !errors.Is(err, ErrUnsupportedPatch) { t.Fatalf("expected ErrUnsupportedPatch, got %v", err) }
}

func TestPatchDecoderRejectsMalformedPayload(t *testing.T) {
	decoder := &PatchDecoder{}
	if _, err := decoder.Decode([]byte(`{"p":"response/fragments","v":"not-an-array"}`)); !errors.Is(err, ErrUnsupportedPatch) {
		t.Fatalf("expected unsupported patch, got %v", err)
	}
}

func TestPatchDecoderRejectsUnknownSSEEvent(t *testing.T) {
	decoder := &PatchDecoder{}
	if _, err := decoder.DecodeEvent("future-event", []byte(`{}`)); !errors.Is(err, ErrUnsupportedPatch) {
		t.Fatalf("expected unsupported event, got %v", err)
	}
}
