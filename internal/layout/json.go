package layout

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
)

// obj is a decoded JSON object whose values are kept raw. Known fields are
// popped from it; whatever remains is the unknown-field remainder (Extra).
type obj map[string]json.RawMessage

func parseObj(b []byte) (obj, error) {
	var o obj
	if err := json.Unmarshal(b, &o); err != nil {
		return nil, err
	}
	if o == nil {
		o = obj{}
	}
	return o, nil
}

// pop decodes and removes key into v. Missing keys and null leave v untouched.
func (o obj) pop(key string, v any) error {
	raw, ok := o[key]
	if !ok {
		return nil
	}
	delete(o, key)
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil
	}
	return json.Unmarshal(raw, v)
}

// extra returns the remaining fields, or nil when there are none.
func (o obj) extra() map[string]json.RawMessage {
	if len(o) == 0 {
		return nil
	}
	return o
}

// encoder assembles an output object from Extra plus known fields.
type encoder obj

func newEncoder(extra map[string]json.RawMessage) encoder {
	e := make(encoder, len(extra)+4)
	for k, v := range extra {
		e[k] = v
	}
	return e
}

func (e encoder) put(key string, v any) error {
	raw, err := json.Marshal(v)
	if err != nil {
		return err
	}
	e[key] = raw
	return nil
}

// bytes marshals the object; encoding/json sorts map keys, so output is
// deterministic.
func (e encoder) bytes() ([]byte, error) { return json.Marshal(map[string]json.RawMessage(e)) }

func cloneExtra(m map[string]json.RawMessage) map[string]json.RawMessage {
	if m == nil {
		return nil
	}
	out := make(map[string]json.RawMessage, len(m))
	for k, v := range m {
		out[k] = append(json.RawMessage(nil), v...)
	}
	return out
}

func cloneRaw(r json.RawMessage) json.RawMessage {
	if r == nil {
		return nil
	}
	return append(json.RawMessage(nil), r...)
}

// Equal reports whether a and b are semantically identical JSON documents:
// object key order and number formatting (268.0 vs 268) do not matter. A nil
// document equals an empty one.
func Equal(a, b *Document) bool {
	return reflect.DeepEqual(generic(a), generic(b))
}

func generic(d *Document) any {
	if d == nil {
		d = &Document{}
	}
	raw, err := d.Encode()
	if err != nil {
		return err.Error()
	}
	var v any
	dec := json.NewDecoder(bytes.NewReader(raw))
	if err := dec.Decode(&v); err != nil {
		return err.Error()
	}
	return v
}

func isBlank(s string) bool { return strings.TrimSpace(s) == "" }
