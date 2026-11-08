package deepseekweb

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
)

var ErrUnsupportedPatch = errors.New("deepseek web: unsupported completion patch")

// PatchUpdate is one normalized upstream event. The three text channels stay
// separate so callers cannot accidentally leak reasoning or search metadata
// into the OpenAI assistant content field.
type PatchUpdate struct {
	ContentDelta      string
	ReasoningDelta    string
	SearchDelta       string
	CompletionTokens  int
	RequestMessageID  string
	ResponseMessageID string
	Title             string
	Done              bool
}

// PatchDecoder is per-upstream-stream state. Do not share one decoder across
// concurrent completions: content-only APPEND patches inherit the active
// fragment channel from the preceding fragment operation.
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

// DecodeFrame connects the generic bounded SSE parser to the DeepSeek-specific
// decoder without losing event names or payload boundaries.
func (d *PatchDecoder) DecodeFrame(frame SSEFrame) (PatchUpdate, error) {
	return d.DecodeEvent(frame.Event, []byte(frame.Data))
}

// DecodeEvent handles lifecycle events and otherwise decodes the event payload
// as a patch. Public references process patch payloads even when an event name
// other than ready/title/close is present.
func (d *PatchDecoder) DecodeEvent(event string, raw []byte) (PatchUpdate, error) {
	if d == nil {
		return PatchUpdate{}, errors.New("deepseek web: nil patch decoder")
	}
	switch event {
	case "ready":
		var ids struct {
			RequestMessageID  json.RawMessage `json:"request_message_id"`
			ResponseMessageID json.RawMessage `json:"response_message_id"`
		}
		if err := json.Unmarshal(raw, &ids); err != nil {
			return PatchUpdate{}, fmt.Errorf("decode DeepSeek ready event: %w", err)
		}
		return PatchUpdate{
			RequestMessageID:  scalarID(ids.RequestMessageID),
			ResponseMessageID: scalarID(ids.ResponseMessageID),
		}, nil
	case "title":
		var title struct {
			Content string `json:"content"`
		}
		if err := json.Unmarshal(raw, &title); err != nil {
			return PatchUpdate{}, fmt.Errorf("decode DeepSeek title event: %w", err)
		}
		return PatchUpdate{Title: title.Content}, nil
	case "close":
		return PatchUpdate{Done: true}, nil
	case "update_session":
		// Session metadata is not a completion patch or terminal signal.
		// Validate the JSON envelope, then leave lineage changes to the
		// verified completion lifecycle rather than committing from metadata.
		var metadata map[string]json.RawMessage
		if err := json.Unmarshal(raw, &metadata); err != nil || metadata == nil {
			return PatchUpdate{}, ErrUnsupportedPatch
		}
		return PatchUpdate{}, nil
	default:
		return d.Decode(raw)
	}
}

func scalarID(raw json.RawMessage) string {
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var n json.Number
	if json.Unmarshal(raw, &n) == nil {
		return n.String()
	}
	return ""
}

// Decode consumes one SSE data payload. Supported patch shapes are based on
// public reference implementations and remain provisional until matched to a
// current, user-authorized sanitized fixture.
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
		if env.Op != "" && env.Op != "APPEND" {
			return PatchUpdate{}, ErrUnsupportedPatch
		}
		return d.decodeInitialOrAppend(env.Value)
	}

	switch env.Path {
	case "response/fragments":
		if env.Op != "" && env.Op != "APPEND" {
			return PatchUpdate{}, ErrUnsupportedPatch
		}
		var fragments []patchFragment
		if err := json.Unmarshal(env.Value, &fragments); err != nil || len(fragments) == 0 {
			return PatchUpdate{}, ErrUnsupportedPatch
		}
		var result PatchUpdate
		for _, fragment := range fragments {
			update, err := d.setFragment(fragment)
			if err != nil {
				return PatchUpdate{}, err
			}
			mergePatchUpdate(&result, update)
		}
		return result, nil
	case "response/fragments/-1/content":
		if env.Op != "" && env.Op != "APPEND" {
			return PatchUpdate{}, ErrUnsupportedPatch
		}
		var content string
		if err := json.Unmarshal(env.Value, &content); err != nil {
			return PatchUpdate{}, ErrUnsupportedPatch
		}
		return d.channelDelta(content), nil
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
		if err := json.Unmarshal(env.Value, &batch); err != nil || len(batch) == 0 {
			return PatchUpdate{}, ErrUnsupportedPatch
		}
		var result PatchUpdate
		for _, item := range batch {
			if len(item.Value) == 0 {
				return PatchUpdate{}, ErrUnsupportedPatch
			}
			if item.Path == "accumulated_token_usage" {
				var count int
				if err := json.Unmarshal(item.Value, &count); err != nil || count < 0 {
					return PatchUpdate{}, ErrUnsupportedPatch
				}
				result.CompletionTokens = count
				continue
			}
			encoded, err := json.Marshal(item)
			if err != nil {
				return PatchUpdate{}, ErrUnsupportedPatch
			}
			update, err := d.Decode(encoded)
			if err != nil {
				return PatchUpdate{}, err
			}
			mergePatchUpdate(&result, update)
		}
		return result, nil
	case "accumulated_token_usage":
		var count int
		if err := json.Unmarshal(env.Value, &count); err != nil || count < 0 {
			return PatchUpdate{}, ErrUnsupportedPatch
		}
		return PatchUpdate{CompletionTokens: count}, nil
	case "response/search_status":
		return PatchUpdate{}, nil
	case "response/search_results":
		var search json.RawMessage
		if err := json.Unmarshal(env.Value, &search); err != nil {
			return PatchUpdate{}, ErrUnsupportedPatch
		}
		return PatchUpdate{SearchDelta: string(search)}, nil
	default:
		return PatchUpdate{}, fmt.Errorf("%w: %s", ErrUnsupportedPatch, env.Path)
	}
}

func (d *PatchDecoder) decodeInitialOrAppend(value json.RawMessage) (PatchUpdate, error) {
	var initial struct {
		Response struct {
			Fragments             []patchFragment `json:"fragments"`
			AccumulatedTokenUsage int             `json:"accumulated_token_usage"`
		} `json:"response"`
	}
	if err := json.Unmarshal(value, &initial); err == nil && len(initial.Response.Fragments) > 0 {
		var result PatchUpdate
		for _, fragment := range initial.Response.Fragments {
			update, err := d.setFragment(fragment)
			if err != nil {
				return PatchUpdate{}, err
			}
			mergePatchUpdate(&result, update)
		}
		if initial.Response.AccumulatedTokenUsage >= 0 && initial.Response.AccumulatedTokenUsage > 0 {
			result.CompletionTokens = initial.Response.AccumulatedTokenUsage
		}
		return result, nil
	}
	var content string
	if err := json.Unmarshal(value, &content); err == nil {
		return d.channelDelta(content), nil
	}
	return PatchUpdate{}, ErrUnsupportedPatch
}

func (d *PatchDecoder) channelDelta(content string) PatchUpdate {
	switch d.fragmentType {
	case "RESPONSE", "ANSWER":
		return PatchUpdate{ContentDelta: content}
	case "SEARCH", "SEARCH_REF":
		return PatchUpdate{SearchDelta: content}
	default:
		// The audited kittors implementation initializes its active channel
		// to reasoning before the first fragment arrives.
		return PatchUpdate{ReasoningDelta: content}
	}
}

func (d *PatchDecoder) setFragment(fragment patchFragment) (PatchUpdate, error) {
	d.fragmentType = fragment.Type
	switch fragment.Type {
	case "RESPONSE", "ANSWER":
		return PatchUpdate{ContentDelta: fragment.Content}, nil
	case "THINK":
		return PatchUpdate{ReasoningDelta: fragment.Content}, nil
	case "SEARCH", "SEARCH_REF":
		return PatchUpdate{SearchDelta: fragment.Content}, nil
	default:
		return PatchUpdate{}, fmt.Errorf("%w: fragment type %q", ErrUnsupportedPatch, fragment.Type)
	}
}

func mergePatchUpdate(dst *PatchUpdate, src PatchUpdate) {
	dst.ContentDelta += src.ContentDelta
	dst.ReasoningDelta += src.ReasoningDelta
	dst.SearchDelta += src.SearchDelta
	if src.CompletionTokens != 0 {
		dst.CompletionTokens = src.CompletionTokens
	}
	if src.RequestMessageID != "" {
		dst.RequestMessageID = src.RequestMessageID
	}
	if src.ResponseMessageID != "" {
		dst.ResponseMessageID = src.ResponseMessageID
	}
	if src.Title != "" {
		dst.Title = src.Title
	}
	dst.Done = dst.Done || src.Done
}
