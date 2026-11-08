package deepseekweb

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

var ErrUnsupportedOpenAIStreamUpdate = errors.New("deepseek web: unsupported OpenAI stream update")

// OpenAIStreamEncoder converts normalized DeepSeek deltas into the SSE data
// payload expected by OmniRouter's existing Chat Completions gateway. It is
// scoped to one completion and must not be shared across concurrent streams.
type OpenAIStreamEncoder struct {
	id      string
	model   string
	created int64
	finished bool
}

func NewOpenAIStreamEncoder(model string) (*OpenAIStreamEncoder, error) {
	var randomID [12]byte
	if _, err := rand.Read(randomID[:]); err != nil {
		return nil, fmt.Errorf("generate completion ID: %w", err)
	}
	return &OpenAIStreamEncoder{
		id:      "chatcmpl-" + hex.EncodeToString(randomID[:]),
		model:   model,
		created: time.Now().Unix(),
	}, nil
}

// Encode returns one complete SSE data field without the trailing blank line.
// The gateway owns frame separation and emits [DONE] only after the provider
// reports an explicit upstream terminal event.
func (e *OpenAIStreamEncoder) Encode(update PatchUpdate) ([]byte, error) {
	if e == nil {
		return nil, errors.New("deepseek web: nil OpenAI stream encoder")
	}
	if e.finished {
		return nil, errors.New("deepseek web: stream already finished")
	}
	if update.SearchDelta != "" {
		return nil, fmt.Errorf("%w: search metadata requires a dedicated OpenAI extension mapping", ErrUnsupportedOpenAIStreamUpdate)
	}

	delta := make(map[string]string, 2)
	if update.ContentDelta != "" {
		delta["content"] = update.ContentDelta
	}
	if update.ReasoningDelta != "" {
		delta["reasoning_content"] = update.ReasoningDelta
	}
	if len(delta) == 0 && !update.Done {
		return nil, nil
	}

	var finishReason *string
	if update.Done {
		reason := "stop"
		finishReason = &reason
		e.finished = true
	}
	chunk := map[string]any{
		"id":      e.id,
		"object":  "chat.completion.chunk",
		"created": e.created,
		"model":   e.model,
		"choices": []any{
			map[string]any{
				"index":         0,
				"delta":         delta,
				"finish_reason": finishReason,
			},
		},
	}
	encoded, err := json.Marshal(chunk)
	if err != nil {
		return nil, fmt.Errorf("encode OpenAI stream chunk: %w", err)
	}
	return append([]byte("data: "), encoded...), nil
}
