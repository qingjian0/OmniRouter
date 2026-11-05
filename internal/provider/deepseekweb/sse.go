package deepseekweb

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
)

var ErrSSEFrameTooLarge = errors.New("deepseek web: SSE frame exceeds configured limit")

// SSEFrame is a transport-neutral Server-Sent Events frame. It deliberately
// preserves the upstream event name and data without interpreting any private
// DeepSeek payload fields or terminal-event conventions.
type SSEFrame struct {
	Event string
	ID    string
	Retry *int
	Data  string
}

// ParseSSE reads the standard SSE framing format and emits complete frames.
// maxFrameBytes limits accumulated field data for one frame; values <= 0 use
// the conservative default of 1 MiB. The reader is not closed by this function.
func ParseSSE(r io.Reader, maxFrameBytes int, emit func(SSEFrame) error) error {
	if r == nil {
		return errors.New("deepseek web: nil SSE reader")
	}
	if emit == nil {
		return errors.New("deepseek web: nil SSE frame handler")
	}
	if maxFrameBytes <= 0 {
		maxFrameBytes = 1 << 20
	}

	reader := bufio.NewReader(r)
	var frame SSEFrame
	var data []string
	frameBytes := 0

	dispatch := func() error {
		if len(data) == 0 {
			frame = SSEFrame{}
			frameBytes = 0
			return nil
		}
		frame.Data = strings.Join(data, "\n")
		if err := emit(frame); err != nil {
			return err
		}
		frame = SSEFrame{}
		data = nil
		frameBytes = 0
		return nil
	}

	for {
		line, err := reader.ReadString('\n')
		if len(line) > 0 {
			line = strings.TrimSuffix(line, "\n")
			line = strings.TrimSuffix(line, "\r")
			if line == "" {
				if dispatchErr := dispatch(); dispatchErr != nil {
					return dispatchErr
				}
			} else if !strings.HasPrefix(line, ":") {
				field, value, found := strings.Cut(line, ":")
				if !found {
					value = ""
				} else {
					value = strings.TrimPrefix(value, " ")
				}
				frameBytes += len(line)
				if frameBytes > maxFrameBytes {
					return ErrSSEFrameTooLarge
				}
				switch field {
				case "event":
					frame.Event = value
				case "id":
					if !strings.ContainsRune(value, '\x00') {
						frame.ID = value
					}
				case "retry":
					if n, parseErr := strconv.Atoi(value); parseErr == nil && n >= 0 {
						frame.Retry = &n
					}
				case "data":
					data = append(data, value)
				}
			}
		}

		if err != nil {
			if errors.Is(err, io.EOF) {
				if dispatchErr := dispatch(); dispatchErr != nil {
					return dispatchErr
				}
				return nil
			}
			if len(line) > 0 {
				return fmt.Errorf("deepseek web: read SSE frame: %w", err)
			}
			return fmt.Errorf("deepseek web: read SSE stream: %w", err)
		}
	}
}
