package anthropic

import "bytes"

// UnsupportedControlError contains only a fixed protocol field name, never its supplied value.
type UnsupportedControlError struct{ control string }

func (e *UnsupportedControlError) Error() string {
	return "Request control " + e.control + " is not supported by the Kiro ACP adapter"
}
func (e *UnsupportedControlError) Control() string { return e.control }
func (e *UnsupportedControlError) Unwrap() error   { return ErrRequest }

// ValidateControls rejects known constraints with no implemented ACP mapping. It is also used at
// internal session admission, so rejection cannot evict a binding or consume pending tool results.
// This is a partial support boundary: reasoning, structured output and token limits have separate
// unresolved semantics documented in D33. Unknown compatible fields remain in Extra.
func (r *Request) ValidateControls() error {
	if r == nil {
		return ErrRequest
	}
	for _, control := range []string{"mcp_servers", "container", "inference_geo", "service_tier", "stop_sequences", "temperature", "top_p", "top_k"} {
		raw, present := r.Extra[control]
		if !present {
			continue
		}
		raw = bytes.Trim(raw, " \t\r\n")
		switch control {
		case "container", "inference_geo":
			if bytes.Equal(raw, []byte("null")) {
				continue
			}
		case "mcp_servers", "stop_sequences":
			if len(raw) >= 2 && raw[0] == '[' && raw[len(raw)-1] == ']' && len(bytes.Trim(raw[1:len(raw)-1], " \t\r\n")) == 0 {
				continue
			}
		}
		return &UnsupportedControlError{control: control}
	}
	return nil
}
