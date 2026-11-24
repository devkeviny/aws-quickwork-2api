package proxy

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestIsGuardrailBlockText(t *testing.T) {
	blocked := "That content did not pass our automated check. Try asking again - a small rephrase usually helps."
	if !isGuardrailBlockText(blocked) {
		t.Errorf("expected the upstream refusal text to match")
	}
	if !isGuardrailBlockText(strings.ToUpper(blocked)) {
		t.Errorf("expected matching to be case-insensitive")
	}
	if isGuardrailBlockText("Here is the summary you asked for.") {
		t.Errorf("normal completion text must not match")
	}
	if isGuardrailBlockText("") {
		t.Errorf("empty text must not match")
	}
}

func TestErrorTextEmptyWhenNoDetail(t *testing.T) {
	if got := errorText(nil, "", ""); got != "" {
		t.Errorf("errorText with no detail = %q, want empty", got)
	}
	if got := errorText(json.RawMessage(`{"message":"blocked by policy"}`), "", ""); got != "blocked by policy" {
		t.Errorf("errorText from payload = %q, want %q", got, "blocked by policy")
	}
	if got := errorText(nil, "fallback message", ""); got != "fallback message" {
		t.Errorf("errorText from fallback = %q, want %q", got, "fallback message")
	}
}

func TestErrGuardrailBlockedIsContentError(t *testing.T) {
	err := ErrGuardrailBlocked
	if !errors.Is(err, ErrGuardrailBlocked) {
		t.Errorf("sentinel must match itself")
	}
	if !isContentError(err) {
		t.Errorf("guardrail error must be a content error (fail fast, no account blame)")
	}
	if !isContentError(ErrUserMessageTooLarge) {
		t.Errorf("too-large error must stay a content error")
	}
	if isContentError(errors.New("upstream WebSocket read: EOF")) {
		t.Errorf("transport errors must not be content errors")
	}
}
