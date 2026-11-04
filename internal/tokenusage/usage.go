// Package tokenusage parses provider-reported token accounting without
// estimating missing values. A nil token count means the provider did not
// report that value; a pointer to zero is an explicitly reported zero.
package tokenusage

import (
	"bytes"
	"encoding/json"
	"fmt"
)

type Usage struct {
	InputTokens  *int64 `json:"input_tokens,omitempty"`
	OutputTokens *int64 `json:"output_tokens,omitempty"`
	TotalTokens  *int64 `json:"total_tokens,omitempty"`
	Source       string `json:"source"`
	Estimated    bool   `json:"estimated"`
	Final        bool   `json:"final"`
}

// ParseOpenAI extracts the usage object from a non-streaming Chat Completions
// response or one SSE data payload. It intentionally does not infer totals.
func ParseOpenAI(data []byte, final bool) (*Usage, error) {
	var envelope struct {
		Usage *struct {
			Prompt     *int64 `json:"prompt_tokens"`
			Completion *int64 `json:"completion_tokens"`
			Total      *int64 `json:"total_tokens"`
		} `json:"usage"`
	}
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("[DONE]")) {
		return nil, nil
	}
	if err := json.Unmarshal(trimmed, &envelope); err != nil {
		return nil, fmt.Errorf("decode OpenAI usage envelope: %w", err)
	}
	if envelope.Usage == nil {
		return nil, nil
	}
	if err := validateNonNegative("prompt_tokens", envelope.Usage.Prompt); err != nil {
		return nil, err
	}
	if err := validateNonNegative("completion_tokens", envelope.Usage.Completion); err != nil {
		return nil, err
	}
	if err := validateNonNegative("total_tokens", envelope.Usage.Total); err != nil {
		return nil, err
	}
	return &Usage{InputTokens: envelope.Usage.Prompt, OutputTokens: envelope.Usage.Completion, TotalTokens: envelope.Usage.Total, Source: "provider", Estimated: false, Final: final}, nil
}

// Merge replaces only values explicitly present in the newer provider event.
// This supports providers that send partial usage before a final usage frame.
func Merge(current, next *Usage) *Usage {
	if next == nil {
		return current
	}
	if current == nil {
		current = &Usage{Source: next.Source, Estimated: next.Estimated}
	}
	if next.InputTokens != nil {
		current.InputTokens = next.InputTokens
	}
	if next.OutputTokens != nil {
		current.OutputTokens = next.OutputTokens
	}
	if next.TotalTokens != nil {
		current.TotalTokens = next.TotalTokens
	}
	if next.Source != "" {
		current.Source = next.Source
	}
	current.Estimated = current.Estimated || next.Estimated
	current.Final = current.Final || next.Final
	return current
}

// ParseAnthropic extracts the native Messages API usage envelope.
func ParseAnthropic(data []byte, final bool) (*Usage, error) {
	var envelope struct {
		Usage *struct {
			Input  *int64 `json:"input_tokens"`
			Output *int64 `json:"output_tokens"`
		} `json:"usage"`
		Message *struct {
			Usage *struct {
				Input  *int64 `json:"input_tokens"`
				Output *int64 `json:"output_tokens"`
			} `json:"usage"`
		} `json:"message"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(data), &envelope); err != nil {
		return nil, fmt.Errorf("decode Anthropic usage envelope: %w", err)
	}
	usage := envelope.Usage
	if usage == nil && envelope.Message != nil {
		usage = envelope.Message.Usage
	}
	if usage == nil {
		return nil, nil
	}
	if err := validateNonNegative("input_tokens", usage.Input); err != nil {
		return nil, err
	}
	if err := validateNonNegative("output_tokens", usage.Output); err != nil {
		return nil, err
	}
	var total *int64
	if usage.Input != nil && usage.Output != nil {
		if *usage.Input > int64(^uint64(0)>>1)-*usage.Output {
			return nil, fmt.Errorf("Anthropic token usage total overflows int64")
		}
		value := *usage.Input + *usage.Output
		total = &value
	}
	return &Usage{InputTokens: usage.Input, OutputTokens: usage.Output, TotalTokens: total, Source: "provider", Final: final}, nil
}

// ParseGemini extracts native Gemini usageMetadata. Cached and modality-specific
// details are intentionally not folded into the aggregate input/output counts.
func ParseGemini(data []byte, final bool) (*Usage, error) {
	var envelope struct {
		Usage *struct {
			Prompt *int64 `json:"promptTokenCount"`
			Output *int64 `json:"candidatesTokenCount"`
			Total  *int64 `json:"totalTokenCount"`
		} `json:"usageMetadata"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(data), &envelope); err != nil {
		return nil, fmt.Errorf("decode Gemini usage envelope: %w", err)
	}
	if envelope.Usage == nil {
		return nil, nil
	}
	if err := validateNonNegative("promptTokenCount", envelope.Usage.Prompt); err != nil {
		return nil, err
	}
	if err := validateNonNegative("candidatesTokenCount", envelope.Usage.Output); err != nil {
		return nil, err
	}
	if err := validateNonNegative("totalTokenCount", envelope.Usage.Total); err != nil {
		return nil, err
	}
	return &Usage{InputTokens: envelope.Usage.Prompt, OutputTokens: envelope.Usage.Output, TotalTokens: envelope.Usage.Total, Source: "provider", Final: final}, nil
}

func validateNonNegative(field string, value *int64) error {
	if value != nil && *value < 0 {
		return fmt.Errorf("provider token usage field %s must not be negative", field)
	}
	return nil
}
