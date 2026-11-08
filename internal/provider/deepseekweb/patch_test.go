package deepseekweb

import (
	"errors"
	"strings"
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

func TestPatchDecoderDefaultsUnclassifiedAppendToReasoning(t *testing.T) {
	decoder := &PatchDecoder{}
	update, err := decoder.Decode([]byte(`{"o":"APPEND","v":"thinking text"}`))
	if err != nil {
		t.Fatal(err)
	}
	if update.ReasoningDelta != "thinking text" || update.ContentDelta != "" {
		t.Fatalf("default append channel mismatch: %+v", update)
	}
}

func TestPatchDecoderTracksReasoningSeparately(t *testing.T) {
	decoder := &PatchDecoder{}
	thinking, err := decoder.Decode([]byte(`{"p":"response/fragments","o":"APPEND","v":[{"type":"THINK","content":"private reasoning"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if thinking.ContentDelta != "" || thinking.ReasoningDelta != "private reasoning" {
		t.Fatalf("reasoning was not kept separate: %+v", thinking)
	}
	appendThinking, err := decoder.Decode([]byte(`{"p":"response/fragments/-1/content","o":"APPEND","v":" more private reasoning"}`))
	if err != nil {
		t.Fatal(err)
	}
	if appendThinking.ContentDelta != "" || appendThinking.ReasoningDelta != " more private reasoning" {
		t.Fatalf("reasoning append was not kept separate: %+v", appendThinking)
	}
	answer, err := decoder.Decode([]byte(`{"p":"response/fragments","o":"APPEND","v":[{"type":"RESPONSE","content":"public"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if answer.ContentDelta != "public" || answer.ReasoningDelta != "" {
		t.Fatalf("response fragment missing: %+v", answer)
	}
}

func TestPatchDecoderSupportsAnswerAliasAndAllFragments(t *testing.T) {
	decoder := &PatchDecoder{}
	update, err := decoder.Decode([]byte(`{"p":"response/fragments","o":"APPEND","v":[{"type":"THINK","content":"thought"},{"type":"ANSWER","content":"answer"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if update.ReasoningDelta != "thought" || update.ContentDelta != "answer" {
		t.Fatalf("all fragment channels should be preserved: %+v", update)
	}
}

func TestPatchDecoderKeepsSearchChannelSeparate(t *testing.T) {
	decoder := &PatchDecoder{}
	update, err := decoder.Decode([]byte(`{"p":"response/fragments","o":"APPEND","v":[{"type":"SEARCH","content":"search metadata"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if update.SearchDelta != "search metadata" || update.ContentDelta != "" || update.ReasoningDelta != "" {
		t.Fatalf("search metadata channel mismatch: %+v", update)
	}
	appendUpdate, err := decoder.Decode([]byte(`{"p":"response/fragments/-1/content","o":"APPEND","v":" more search metadata"}`))
	if err != nil {
		t.Fatal(err)
	}
	if appendUpdate.SearchDelta != " more search metadata" {
		t.Fatalf("search append channel mismatch: %+v", appendUpdate)
	}
}

func TestPatchDecoderMapsHintErrorsToTypedFailures(t *testing.T) {
	tests := []struct {
		name string
		data string
		want error
	}{
		{name: "policy blocked", data: `{"type":"error","finish_reason":"policy_blocked"}`, want: ErrPolicyBlocked},
		{name: "rate limited", data: `{"type":"error","finish_reason":"rate_limit_reached"}`, want: ErrRateLimited},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			decoder := &PatchDecoder{}
			_, err := decoder.DecodeEvent("hint", []byte(test.data))
			if !errors.Is(err, test.want) {
				t.Fatalf("hint error=%v, want errors.Is(_, %v)", err, test.want)
			}
		})
	}
}

func TestPatchDecoderRejectsMalformedHintError(t *testing.T) {
	decoder := &PatchDecoder{}
	_, err := decoder.DecodeEvent("hint", []byte(`not-json`))
	if !errors.Is(err, ErrUnsupportedPatch) {
		t.Fatalf("malformed hint must fail closed, got %v", err)
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

func TestPatchDecoderSSEEventLifecycle(t *testing.T) {
	decoder := &PatchDecoder{}
	ready, err := decoder.DecodeEvent("ready", []byte(`{"request_message_id":"req-1","response_message_id":"resp-1"}`))
	if err != nil {
		t.Fatal(err)
	}
	if ready.RequestMessageID != "req-1" || ready.ResponseMessageID != "resp-1" {
		t.Fatalf("ready IDs not decoded: %+v", ready)
	}
	title, err := decoder.DecodeEvent("title", []byte(`{"content":"A title"}`))
	if err != nil {
		t.Fatal(err)
	}
	if title.Title != "A title" {
		t.Fatalf("title not decoded: %+v", title)
	}
	closed, err := decoder.DecodeEvent("close", []byte(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if !closed.Done {
		t.Fatalf("close event must terminate stream: %+v", closed)
	}
}

func TestPatchDecoderTreatsUpdateSessionAsNonTerminalMetadata(t *testing.T) {
	decoder := &PatchDecoder{}
	update, err := decoder.DecodeEvent("update_session", []byte(`{"chat_session_id":"session-1"}`))
	if err != nil {
		t.Fatal(err)
	}
	if update.Done || update.ContentDelta != "" || update.ReasoningDelta != "" || update.ResponseMessageID != "" {
		t.Fatalf("session metadata must not become completion output or terminal state: %+v", update)
	}
	if _, err := decoder.DecodeEvent("update_session", []byte(`not-json`)); !errors.Is(err, ErrUnsupportedPatch) {
		t.Fatalf("malformed session metadata must fail closed, got %v", err)
	}
}

func TestPatchDecoderDecodesNumericMessageIDs(t *testing.T) {
	decoder := &PatchDecoder{}
	ready, err := decoder.DecodeEvent("ready", []byte(`{"request_message_id":123,"response_message_id":456}`))
	if err != nil {
		t.Fatal(err)
	}
	if ready.RequestMessageID != "123" || ready.ResponseMessageID != "456" {
		t.Fatalf("numeric IDs not normalized: %+v", ready)
	}
}

func TestPatchDecoderDecodesPatchOnOtherNamedSSEEvent(t *testing.T) {
	decoder := &PatchDecoder{}
	update, err := decoder.DecodeEvent("message", []byte(`{"p":"response/fragments","o":"APPEND","v":[{"type":"RESPONSE","content":"answer"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if update.ContentDelta != "answer" {
		t.Fatalf("patch was not decoded: %+v", update)
	}
}

func TestPatchDecoderDecodesSSEFrame(t *testing.T) {
	decoder := &PatchDecoder{}
	update, err := decoder.DecodeFrame(SSEFrame{
		Event: "message",
		Data:  `{"p":"response/fragments","o":"APPEND","v":[{"type":"RESPONSE","content":"frame answer"}]}`,
	})
	if err != nil {
		t.Fatal(err)
	}
	if update.ContentDelta != "frame answer" {
		t.Fatalf("frame decode mismatch: %+v", update)
	}
}

func TestPatchDecoderDoesNotTreatDoneSentinelAsTerminal(t *testing.T) {
	decoder := &PatchDecoder{}
	update, err := decoder.Decode([]byte("[DONE]"))
	if err != nil {
		t.Fatal(err)
	}
	if update.Done {
		t.Fatal("[DONE] is not a verified DeepSeek terminal event")
	}
}

func TestPatchDecoderPreservesSearchResultsAsMetadata(t *testing.T) {
	decoder := &PatchDecoder{}
	update, err := decoder.Decode([]byte(`{"p":"response/search_results","o":"APPEND","v":[{"title":"example","url":"https://example.test"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if update.SearchDelta == "" || update.ContentDelta != "" || update.ReasoningDelta != "" {
		t.Fatalf("search metadata channel mismatch: %+v", update)
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

func TestPatchDecoderRejectsUnknownFragmentType(t *testing.T) {
	decoder := &PatchDecoder{}
	_, err := decoder.Decode([]byte(`{"p":"response/fragments","o":"APPEND","v":[{"type":"FUTURE_PRIVATE_TYPE","content":"x"}]}`))
	if !errors.Is(err, ErrUnsupportedPatch) {
		t.Fatalf("expected unsupported patch, got %v", err)
	}
}

func TestParseAndDecodeDeepSeekSSELifecycle(t *testing.T) {
	input := strings.Join([]string{
		"event: ready",
		`data: {"request_message_id":"req-1","response_message_id":"resp-1"}`,
		"",
		"event: message",
		`data: {"p":"response/fragments","o":"APPEND","v":[{"type":"THINK","content":"reason"}]}`,
		"",
		"event: message",
		`data: {"p":"response/fragments","o":"APPEND","v":[{"type":"RESPONSE","content":"answer"}]}`,
		"",
		"event: close",
		"data: {}",
		"",
	}, "\n")
	decoder := &PatchDecoder{}
	var updates []PatchUpdate
	if err := ParseSSE(strings.NewReader(input), 4096, func(frame SSEFrame) error {
		update, err := decoder.DecodeFrame(frame)
		if err != nil {
			return err
		}
		updates = append(updates, update)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(updates) != 4 {
		t.Fatalf("got %d updates, want 4: %+v", len(updates), updates)
	}
	if updates[0].RequestMessageID != "req-1" || updates[0].ResponseMessageID != "resp-1" {
		t.Fatalf("ready event lineage mismatch: %+v", updates[0])
	}
	if updates[1].ReasoningDelta != "reason" || updates[1].ContentDelta != "" {
		t.Fatalf("reasoning channel mismatch: %+v", updates[1])
	}
	if updates[2].ContentDelta != "answer" || updates[2].ReasoningDelta != "" {
		t.Fatalf("answer channel mismatch: %+v", updates[2])
	}
	if !updates[3].Done {
		t.Fatalf("close event did not terminate stream: %+v", updates[3])
	}
}

func TestPatchDecoderBatchPreservesFragmentUpdates(t *testing.T) {
	decoder := &PatchDecoder{}
	update, err := decoder.Decode([]byte(`{"p":"response","o":"BATCH","v":[{"p":"response/fragments","o":"APPEND","v":[{"type":"THINK","content":"reason"},{"type":"RESPONSE","content":"answer"}]},{"p":"accumulated_token_usage","v":17},{"p":"response/status","o":"SET","v":"FINISHED"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if update.ReasoningDelta != "reason" || update.ContentDelta != "answer" || update.CompletionTokens != 17 || !update.Done {
		t.Fatalf("batch updates were not fully merged: %+v", update)
	}
}

func TestPatchDecoderBatchRejectsUnknownPatch(t *testing.T) {
	decoder := &PatchDecoder{}
	_, err := decoder.Decode([]byte(`{"p":"response","o":"BATCH","v":[{"p":"response/unknown","o":"SET","v":"x"}]}`))
	if !errors.Is(err, ErrUnsupportedPatch) {
		t.Fatalf("unknown batch patch must fail closed, got %v", err)
	}
}

func TestPatchDecoderReadsStandaloneTokenUsage(t *testing.T) {
	decoder := &PatchDecoder{}
	update, err := decoder.Decode([]byte(`{"p":"accumulated_token_usage","o":"SET","v":9}`))
	if err != nil {
		t.Fatal(err)
	}
	if update.CompletionTokens != 9 {
		t.Fatalf("token usage mismatch: %+v", update)
	}
}
