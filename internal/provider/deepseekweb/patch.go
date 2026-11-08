package deepseekweb

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
)

var ErrUnsupportedPatch = errors.New("deepseek web: unsupported completion patch")

// PatchUpdate is the user-visible effect of one observed DeepSeek JSON-Patch
// operation. THINK fragments are tracked but never exposed as assistant text.
type PatchUpdate struct {
	ContentDelta    string
	CompletionTokens int
	Done             bool
}

type PatchDecoder struct {
	fragmentType string
}

type patchEnvelope struct {
	Path  string          `json:"p"`
	Op    string          `json:"o"`
	Value json.RawMessage `json:"v"`
}

type patchFragment struct {
	Type    string `json:"type"`
	Content string `json:"content"`
}

// Decode consumes one SSE data payload. The supported operations are based on
// public reference implementations and remain provisional until matched to a
// current, user-authorized sanitized fixture. Unknown structural operations
// fail closed rather than silently producing a truncated completion.
func (d *PatchDecoder) Decode(raw []byte) (PatchUpdate, error) {
	if d == nil {
		return PatchUpdate{}, errors.New("deepseek web: nil patch decoder")
	}
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || bytes.Equal(raw, []byte("[DONE]")) {
		return PatchUpdate{}, nil
	}
	var env patchEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return PatchUpdate{}, fmt.Errorf("decode DeepSeek patch envelope: %w", err)
	}
	if len(env.Value) == 0 {
		return PatchUpdate{}, ErrUnsupportedPatch
	}
	if env.Path == "" {
		return d.decodeInitialOrAppend(env.Value)
	}

	switch env.Path {
	case "response/fragments":
		var fragments []patchFragment
		if err := json.Unmarshal(env.Value, &fragments); err != nil || len(fragments) == 0 {
			return PatchUpdate{}, ErrUnsupportedPatch
		}
		d.fragmentType = fragments[0].Type
		return fragmentUpdate(fragments[0])
	case "response/fragments/-1/content":
		var content string
		if err := json.Unmarshal(env.Value, &content); err != nil {
			return PatchUpdate{}, ErrUnsupportedPatch
		}
		if d.fragmentType == "RESPONSE" {
			return PatchUpdate{ContentDelta: content}, nil
		}
		if d.fragmentType == "THINK" {
			return PatchUpdate{}, nil
		}
		return PatchUpdate{}, ErrUnsupportedPatch
	case "response/status", "quasi_status":
		var status string
		if err := json.Unmarshal(env.Value, &status); err != nil {
			return PatchUpdate{}, ErrUnsupportedPatch
		}
		return PatchUpdate{Done: status == "FINISHED"}, nil
	case "response":
		if env.Op != "BATCH" {
			return PatchUpdate{}, ErrUnsupportedPatch
		}
		var batch []patchEnvelope
		if err := json.Unmarshal(env.Value, &batch); err != nil {
			return PatchUpdate{}, ErrUnsupportedPatch
		}
		var result PatchUpdate
		for _, item := range batch {
			switch item.Path {
			case "quasi_status":
				var status string
				if err := json.Unmarshal(item.Value, &status); err != nil {
					return PatchUpdate{}, ErrUnsupportedPatch
				}
				result.Done = result.Done || status == "FINISHED"
			case "accumulated_token_usage":
				var count int
				if err := json.Unmarshal(item.Value, &count); err != nil || count < 0 {
					return PatchUpdate{}, ErrUnsupportedPatch
				}
				result.CompletionTokens = count
			}
		}
		return result, nil
	default:
		return PatchUpdate{}, fmt.Errorf("%w: %s", ErrUnsupportedPatch, env.Path)
	}
}

func (d *PatchDecoder) decodeInitialOrAppend(value json.RawMessage) (PatchUpdate, error) {
	var initial struct {
		Response struct {
			Fragments []patchFragment `json:"fragments"`
		} `json:"response"`
	}
	if err := json.Unmarshal(value, &initial); err == nil && len(initial.Response.Fragments) > 0 {
		d.fragmentType = initial.Response.Fragments[0].Type
		return fragmentUpdate(initial.Response.Fragments[0])
	}
	var content string
	if err := json.Unmarshal(value, &content); err == nil {
		switch d.fragmentType {
		case "RESPONSE":
			return PatchUpdate{ContentDelta: content}, nil
		case "THINK":
			return PatchUpdate{}, nil
		default:
			return PatchUpdate{}, ErrUnsupportedPatch
		}
	}
	return PatchUpdate{}, ErrUnsupportedPatch
}

func fragmentUpdate(fragment patchFragment) (PatchUpdate, error) {
	switch fragment.Type {
	case "RESPONSE":
		return PatchUpdate{ContentDelta: fragment.Content}, nil
	case "THINK":
		return PatchUpdate{}, nil
	default:
		return PatchUpdate{}, ErrUnsupportedPatch
	}
}
