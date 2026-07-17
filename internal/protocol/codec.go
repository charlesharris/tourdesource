package protocol

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
)

// Encoder writes protocol messages as newline-delimited JSON.
type Encoder struct {
	w io.Writer
}

// NewEncoder returns an Encoder writing to w (e.g. a provider's stdin).
func NewEncoder(w io.Writer) *Encoder { return &Encoder{w: w} }

// Encode marshals v to a single JSON line terminated by '\n'. A message is
// always exactly one line, so encoded values must not contain raw newlines —
// encoding/json escapes them, so this holds for arbitrary payloads.
func (e *Encoder) Encode(v any) error {
	raw, err := json.Marshal(v)
	if err != nil {
		return err
	}
	raw = append(raw, '\n')
	_, err = e.w.Write(raw)
	return err
}

// Decoder reads newline-delimited JSON messages. It handles arbitrarily long
// lines (bufio.Scanner's token cap would truncate large inline results).
type Decoder struct {
	r *bufio.Reader
}

// NewDecoder returns a Decoder reading from r (e.g. a provider's stdout).
func NewDecoder(r io.Reader) *Decoder { return &Decoder{r: bufio.NewReader(r)} }

// nextLine returns the next non-empty line, or io.EOF at end of stream.
func (d *Decoder) nextLine() ([]byte, error) {
	for {
		line, err := d.r.ReadBytes('\n')
		trimmed := bytes.TrimSpace(line)
		if len(trimmed) > 0 {
			return trimmed, nil
		}
		if err != nil {
			return nil, err
		}
	}
}

// DecodeRequest reads and decodes the next request (provider side).
func (d *Decoder) DecodeRequest() (*Request, error) {
	line, err := d.nextLine()
	if err != nil {
		return nil, err
	}
	var req Request
	if err := json.Unmarshal(line, &req); err != nil {
		return nil, err
	}
	return &req, nil
}

// DecodeResponse reads and decodes the next response (core side).
func (d *Decoder) DecodeResponse() (*Response, error) {
	line, err := d.nextLine()
	if err != nil {
		return nil, err
	}
	var resp Response
	if err := json.Unmarshal(line, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}
