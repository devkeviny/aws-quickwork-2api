package proxy

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
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

// The upstream retry rewrite (343d347f) routes all failures through
// retryActionFor/invalidChatRequest/chatErrorStatus. A guardrail block must
// still fail fast, never blame the account, and surface as 502
// guardrail_blocked — not 400, not upstream_error.
func TestGuardrailBlockedRetryWiring(t *testing.T) {
	// finish() wraps the sentinel with the refusal detail.
	wrapped := fmt.Errorf("%w: %s", ErrGuardrailBlocked, "That content did not pass our automated check.")
	if got := retryActionFor(wrapped); got != retryStop {
		t.Errorf("retryActionFor(guardrail) = %v, want retryStop", got)
	}
	if !invalidChatRequest(wrapped) {
		t.Errorf("invalidChatRequest(guardrail) = false, want true (account must not be blamed)")
	}
	status, code := chatErrorStatus(wrapped)
	if status != 502 || code != "guardrail_blocked" {
		t.Errorf("chatErrorStatus(guardrail) = (%d, %q), want (502, \"guardrail_blocked\")", status, code)
	}
	// A real transient transport error must keep retrying and keep the old
	// mapping. (A plain string error is unclassifiable and stops by design.)
	transport := io.EOF
	if got := retryActionFor(transport); got == retryStop {
		t.Errorf("retryActionFor(io.EOF) = retryStop, want a retry action")
	}
	if invalidChatRequest(transport) {
		t.Errorf("invalidChatRequest(transport) = true, want false")
	}
	status, code = chatErrorStatus(transport)
	if status != 502 || code != "upstream_error" {
		t.Errorf("chatErrorStatus(transport) = (%d, %q), want (502, \"upstream_error\")", status, code)
	}
}
