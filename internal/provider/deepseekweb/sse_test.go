package deepseekweb

import (
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"
)

func TestParseSSEPreservesStandardFrameFields(t *testing.T) {
	input := ": keepalive\r\nevent: update\r\nid: 42\r\nretry: 1500\r\ndata: first\r\ndata:second\r\n\r\ndata: [DONE]"
	var got []SSEFrame
	if err := ParseSSE(strings.NewReader(input), 1024, func(frame SSEFrame) error {
		got = append(got, frame)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d frames, want 2: %#v", len(got), got)
	}
	if got[0].Event != "update" || got[0].ID != "42" || got[0].Retry == nil || *got[0].Retry != 1500 || got[0].Data != "first\nsecond" {
		t.Fatalf("unexpected first frame: %#v", got[0])
	}
	if got[1].Data != "[DONE]" || got[1].Event != "" {
		t.Fatalf("unexpected second frame: %#v", got[1])
	}
}

func TestParseSSEIgnoresCommentsAndFramesWithoutData(t *testing.T) {
	var got []SSEFrame
	err := ParseSSE(strings.NewReader(": ping\nevent: metadata\nid: 1\n\ndata: value\n"), 128, func(frame SSEFrame) error {
		got = append(got, frame)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, []SSEFrame{{Data: "value"}}) {
		t.Fatalf("unexpected frames: %#v", got)
	}
}

func TestParseSSERejectsOversizedLineAndFrame(t *testing.T) {
	for _, input := range []string{
		"data: " + strings.Repeat("x", 64) + "\n\n",
		"data: 123456789\ndata: 123456789\n\n",
	} {
		err := ParseSSE(strings.NewReader(input), 24, func(SSEFrame) error { return nil })
		if !errors.Is(err, ErrSSEFrameTooLarge) {
			t.Fatalf("expected size error, got %v", err)
		}
	}
}

func TestParseSSEPropagatesHandlerError(t *testing.T) {
	want := errors.New("consumer stopped")
	err := ParseSSE(strings.NewReader("data: item\n\n"), 128, func(SSEFrame) error { return want })
	if !errors.Is(err, want) {
		t.Fatalf("got %v, want %v", err, want)
	}
}

type brokenReader struct{}

func (brokenReader) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }

func TestParseSSEPropagatesReadError(t *testing.T) {
	err := ParseSSE(brokenReader{}, 128, func(SSEFrame) error { return nil })
	if !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("got %v, want unexpected EOF", err)
	}
}
