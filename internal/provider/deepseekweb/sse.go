package deepseekweb

import (
	"bufio"
	"bytes"
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

// ParseSSE reads standard SSE framing and emits complete frames. maxFrameBytes
// bounds both individual lines and accumulated fields for one frame; values
// <= 0 use a conservative default of 1 MiB. The reader is not closed.
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

	reader := bufio.NewReaderSize(r, 4096)
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
		line, readErr := readSSELine(reader, maxFrameBytes)
		if len(line) > 0 {
			line = bytes.TrimSuffix(line, []byte("\n"))
			line = bytes.TrimSuffix(line, []byte("\r"))
			if len(line) == 0 {
				if err := dispatch(); err != nil {
					return err
				}
			} else if line[0] != ':' {
				field, value, found := bytes.Cut(line, []byte(":"))
				if !found {
					value = nil
				} else {
					value = bytes.TrimPrefix(value, []byte(" "))
				}
				frameBytes += len(line)
				if frameBytes > maxFrameBytes {
					return ErrSSEFrameTooLarge
				}
				switch string(field) {
				case "event":
					frame.Event = string(value)
				case "id":
					if !bytes.ContainsRune(value, '\x00') {
						frame.ID = string(value)
					}
				case "retry":
					if n, err := strconv.Atoi(string(value)); err == nil && n >= 0 {
						frame.Retry = &n
					}
				case "data":
					data = append(data, string(value))
				}
			}
		}

		if readErr != nil {
			if errors.Is(readErr, io.EOF) {
				if err := dispatch(); err != nil {
					return err
				}
				return nil
			}
			if errors.Is(readErr, ErrSSEFrameTooLarge) {
				return readErr
			}
			return fmt.Errorf("deepseek web: read SSE stream: %w", readErr)
		}
	}
}

func readSSELine(reader *bufio.Reader, limit int) ([]byte, error) {
	var line []byte
	for {
		part, err := reader.ReadSlice('\n')
		if len(line)+len(part) > limit {
			return nil, ErrSSEFrameTooLarge
		}
		line = append(line, part...)
		if err == nil {
			return line, nil
		}
		if errors.Is(err, bufio.ErrBufferFull) {
			continue
		}
		if errors.Is(err, io.EOF) {
			return line, io.EOF
		}
		return line, err
	}
}
