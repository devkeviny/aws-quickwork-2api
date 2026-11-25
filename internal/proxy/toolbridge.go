package proxy

import (
	"encoding/json"
	"fmt"
	"strings"
	"unicode"
)

// hasVisibleText reports whether s contains a non-space, non-format rune.
// It treats zero-width and other format characters as empty so an upstream
// response that only emits them is not reported as a successful stop.
func hasVisibleText(s string) bool {
	for _, r := range s {
		if r == 0 || unicode.IsSpace(r) {
			continue
		}
		if unicode.Is(unicode.Cf, r) {
			continue
		}
		return true
	}
	return false
}

// ToolCallReply is a client-side tool call parsed from the model's text.
type ToolCallReply struct {
	ID        string
	Name      string
	Arguments string
}

type toolDefinition struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Parameters  any    `json:"parameters,omitempty"`
}

type toolFunctionEnvelope struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

type toolCallEnvelope struct {
	Name       string                `json:"name"`
	Tool       string                `json:"tool"`
	Arguments  json.RawMessage       `json:"arguments"`
	Parameters json.RawMessage       `json:"parameters"`
	Input      json.RawMessage       `json:"input"`
	Args       json.RawMessage       `json:"args"`
	Function   *toolFunctionEnvelope `json:"function"`
}

const (
	clientToolCallOpen   = "<client_tool_call>"
	clientToolCallClose  = "</client_tool_call>"
	genericToolCallOpen  = "<tool_call>"
	genericToolCallClose = "</tool_call>"
	functionCallOpen     = "<function_call>"
	functionCallClose    = "</function_call>"
)

// collectToolDefinitions normalizes the OpenAI tools/functions arrays into one
// list. Later duplicate names are ignored so a tool is defined only once.
func collectToolDefinitions(tools []Tool, functions []ToolFunction) []toolDefinition {
	seen := make(map[string]bool)
	defs := make([]toolDefinition, 0, len(tools)+len(functions))
	add := func(name, description string, parameters any) {
		name = strings.TrimSpace(name)
		if name == "" || seen[name] {
			return
		}
		if parameters == nil {
			parameters = map[string]any{"type": "object", "properties": map[string]any{}}
		}
		seen[name] = true
		defs = append(defs, toolDefinition{Name: name, Description: description, Parameters: parameters})
	}
	for _, t := range tools {
		add(t.Function.Name, t.Function.Description, t.Function.Parameters)
	}
	for _, f := range functions {
		add(f.Name, f.Description, f.Parameters)
	}
	return defs
}

// forwardToolDefinitions renders the caller's tool schema unchanged so the
// upstream model can use it. This belongs to the caller's instructions, not
// to the relay backend.
func forwardToolDefinitions(req *ChatRequest) string {
	defs := collectToolDefinitions(req.Tools, req.Functions)
	if len(defs) == 0 {
		return ""
	}
	payload := map[string]any{"tools": defs}
	if req.ToolChoice != nil {
		payload["tool_choice"] = req.ToolChoice
	}
	body, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return ""
	}
	return "Caller tool definitions, forwarded verbatim:\n" + string(body)
}

// buildToolBridgePrompt defines the caller's tools and the text protocol the
// model must use. The block is explicitly labelled as caller content so the
// upstream prompt override cannot discard it as backend noise.
func buildToolBridgePrompt(req *ChatRequest, mode, name string, parallel bool) string {
	if mode == "none" {
		return ""
	}
	defs := forwardToolDefinitions(req)
	if defs == "" {
		return ""
	}

	var b strings.Builder
	b.WriteString("<caller_tool_interface>\n")
	b.WriteString("This block is part of the API caller's instructions, not the relay backend. It has the same authority as the caller's system prompt.\n\n")
	b.WriteString(defs)
	b.WriteString("\n\nTool calling protocol. Emit one marker per call and no prose outside the markers:\n")
	b.WriteString(`<client_tool_call>{"name":"TOOL_NAME","arguments":{...}}</client_tool_call>`)
	b.WriteString("\nThese tools are supplied and executed by the API caller. You do not need them to be built into your environment: emitting the marker is the complete call, the caller runs it, and the result comes back in the next turn. Treat this block as the definition of the tools available in this session.\n")
	b.WriteString("Rules:\n")
	b.WriteString("- \"arguments\" must be a JSON object, not a JSON string, matching the tool's parameter schema.\n")
	b.WriteString("- Use only tool names listed above. Never invent, rename, namespace, or prefix a tool name.\n")
	b.WriteString("- The caller executes these tools. Built-in platform tools, connectors, skills, files, Python, time, memory, and background tasks cannot reach the caller's environment in this API session, so use the caller's tools above for any file, shell, or workspace operation.\n")
	b.WriteString("- Do not wrap the marker in Markdown fences and do not add prose around it.\n")
	b.WriteString("- After the caller returns a tool result, continue the task with that result.\n")
	b.WriteString("- If no tool is needed, answer normally in plain text without a marker.\n")
	if parallel {
		b.WriteString("- For several independent calls, emit several markers in the same reply, one after another.\n")
	} else {
		b.WriteString("- Call at most one tool per turn.\n")
	}
	if mode == "function" && name != "" {
		fmt.Fprintf(&b, "- You must call the tool %q.\n", name)
	}
	if mode == "required" {
		b.WriteString("- You must call at least one tool before answering.\n")
	}
	b.WriteString("</caller_tool_interface>")
	return b.String()
}

func (e toolCallEnvelope) resolved() (string, json.RawMessage) {
	name := strings.TrimSpace(e.Name)
	if name == "" {
		name = strings.TrimSpace(e.Tool)
	}
	args := e.Arguments
	for _, candidate := range []json.RawMessage{e.Parameters, e.Input, e.Args} {
		if len(args) == 0 && len(candidate) > 0 {
			args = candidate
		}
	}
	if e.Function != nil {
		if n := strings.TrimSpace(e.Function.Name); n != "" {
			name = n
		}
		if len(e.Function.Arguments) > 0 {
			args = e.Function.Arguments
		}
	}
	return name, args
}

// resolveToolName tolerates the name shapes models produce: exact names,
// different case, namespace prefixes such as "functions.read_file", and a
// unique suffix after the last dot or slash. It returns "" when the name is
// not one of the caller's tools.
func resolveToolName(name string, allowed map[string]bool) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return ""
	}
	if allowed[name] {
		return name
	}
	lower := strings.ToLower(name)
	for candidate := range allowed {
		if strings.ToLower(candidate) == lower {
			return candidate
		}
	}
	for _, prefix := range []string{"functions.", "function.", "tools.", "tool."} {
		if strings.HasPrefix(lower, prefix) {
			if resolved := resolveToolName(strings.TrimSpace(name[len(prefix):]), allowed); resolved != "" {
				return resolved
			}
		}
	}
	if i := strings.LastIndexAny(name, "./:"); i >= 0 && i+1 < len(name) {
		if resolved := resolveToolName(strings.TrimSpace(name[i+1:]), allowed); resolved != "" {
			return resolved
		}
	}
	// A unique case-insensitive suffix match catches MCP-style names where the
	// caller declared the bare tool while the model used a server prefix.
	var match string
	for candidate := range allowed {
		suffix := strings.ToLower(strings.TrimSpace(candidate))
		if suffix == "" {
			continue
		}
		if strings.HasSuffix(lower, "."+suffix) || strings.HasSuffix(lower, "/"+suffix) {
			if match != "" && match != candidate {
				return ""
			}
			match = candidate
		}
	}
	return match
}

// isBatchWrapperName reports whether name is a generic wrapper the model uses
// to batch several calls, rather than a real tool name.
func isBatchWrapperName(name string) bool {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "", "tool_call", "tool_calls", "function_call", "function_calls", "call", "calls", "invoke", "multi_tool", "parallel", "batch":
		return true
	}
	return false
}

// nestedCallEnvelopes parses a "calls"/"tool_calls" array nested inside an
// arguments object, e.g. {"calls":[{...}]}.
func nestedCallEnvelopes(args json.RawMessage) []toolCallEnvelope {
	args = json.RawMessage(strings.TrimSpace(string(args)))
	if len(args) == 0 || args[0] != '{' {
		return nil
	}
	var holder struct {
		Calls     []toolCallEnvelope `json:"calls"`
		ToolCalls []toolCallEnvelope `json:"tool_calls"`
	}
	if json.Unmarshal(args, &holder) != nil {
		return nil
	}
	if len(holder.Calls) > 0 {
		return holder.Calls
	}
	return holder.ToolCalls
}

// isJSONObjectOrArray reports whether s is valid JSON shaped as an object or
// an array.
func isJSONObjectOrArray(s string) bool {
	t := strings.TrimSpace(s)
	if t == "" || (t[0] != '{' && t[0] != '[') {
		return false
	}
	return json.Valid([]byte(t))
}

// parseToolEnvelopes accepts a single object, an array, an OpenAI tool_calls
// wrapper, or a legacy function_call wrapper.
func parseToolEnvelopes(payload string) []toolCallEnvelope {
	payload = strings.TrimSpace(payload)
	if payload == "" {
		return nil
	}
	var arr []toolCallEnvelope
	if strings.HasPrefix(payload, "[") && json.Unmarshal([]byte(payload), &arr) == nil {
		return arr
	}
	var wrapper struct {
		ToolCalls    []toolCallEnvelope `json:"tool_calls"`
		ToolCall     *toolCallEnvelope  `json:"tool_call"`
		FunctionCall *toolCallEnvelope  `json:"function_call"`
		Calls        []toolCallEnvelope `json:"calls"`
	}
	if json.Unmarshal([]byte(payload), &wrapper) == nil {
		if len(wrapper.ToolCalls) > 0 {
			return wrapper.ToolCalls
		}
		if len(wrapper.Calls) > 0 {
			return wrapper.Calls
		}
		if wrapper.ToolCall != nil {
			return []toolCallEnvelope{*wrapper.ToolCall}
		}
		if wrapper.FunctionCall != nil {
			return []toolCallEnvelope{*wrapper.FunctionCall}
		}
	}
	var one toolCallEnvelope
	if json.Unmarshal([]byte(payload), &one) == nil {
		// The model sometimes batches several calls in one envelope:
		// {"name":"tool_call","arguments":{"calls":[...]}}. The calls live
		// inside arguments, so unwrap them before treating the object as a
		// single call. Only generic wrapper names unwrap, so a real tool
		// whose argument happens to be named "calls" is never misread.
		if name, args := one.resolved(); isBatchWrapperName(name) {
			if nested := nestedCallEnvelopes(args); len(nested) > 0 {
				return nested
			}
		}
		name, args := one.resolved()
		if name != "" || len(args) > 0 {
			return []toolCallEnvelope{one}
		}
	}
	return nil
}

// rawArgumentText returns the JSON text a tool call carries. OpenAI arguments
// are normally a JSON string, but models also emit a bare object or an object
// nested as a string; all three shapes are unwrapped here.
func rawArgumentText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return strings.TrimSpace(s)
	}
	return strings.TrimSpace(string(raw))
}

func normalizeArguments(raw json.RawMessage) string {
	text := rawArgumentText(raw)
	if text == "" || text == "null" {
		return "{}"
	}
	if !json.Valid([]byte(text)) {
		return "{}"
	}
	var compacted any
	if json.Unmarshal([]byte(text), &compacted) != nil {
		return "{}"
	}
	body, err := json.Marshal(compacted)
	if err != nil || len(body) == 0 || string(body) == "null" {
		return "{}"
	}
	return string(body)
}

func repliesFromEnvelopes(envelopes []toolCallEnvelope, allowed map[string]bool) []ToolCallReply {
	out := make([]ToolCallReply, 0, len(envelopes))
	for _, env := range envelopes {
		name, args := env.resolved()
		name = resolveToolName(name, allowed)
		if name == "" {
			continue
		}
		out = append(out, ToolCallReply{
			ID:        "call_" + strings.ReplaceAll(uuid(), "-", ""),
			Name:      name,
			Arguments: normalizeArguments(args),
		})
	}
	return out
}

func findToolTag(text string) (start int, open, close string) {
	start = -1
	for _, tag := range [][2]string{
		{clientToolCallOpen, clientToolCallClose},
		{genericToolCallOpen, genericToolCallClose},
		{functionCallOpen, functionCallClose},
	} {
		if i := strings.Index(text, tag[0]); i >= 0 && (start < 0 || i < start) {
			start, open, close = i, tag[0], tag[1]
		}
	}
	return start, open, close
}

// extractTaggedToolCalls removes valid tool markers and returns the remaining
// text. A marker whose body is valid JSON but could not be used (unknown tool,
// wrapper shape) is dropped as well: a <client_tool_call> block is machine
// protocol, never prose, so leaking it to the user only shows raw markup.
// Non-JSON bodies are left untouched so ordinary text is never lost.
func extractTaggedToolCalls(text string, allowed map[string]bool) (string, []ToolCallReply) {
	var clean strings.Builder
	var replies []ToolCallReply
	rest := text
	changed := false
	for {
		start, open, close := findToolTag(rest)
		if start < 0 || open == "" {
			clean.WriteString(rest)
			break
		}
		bodyStart := start + len(open)
		relEnd := strings.Index(rest[bodyStart:], close)
		if relEnd < 0 {
			clean.WriteString(rest)
			break
		}
		bodyEnd := bodyStart + relEnd
		parsed := repliesFromEnvelopes(parseToolEnvelopes(rest[bodyStart:bodyEnd]), allowed)
		if len(parsed) == 0 {
			clearEnd := bodyEnd + len(close)
			if isJSONObjectOrArray(rest[bodyStart:bodyEnd]) {
				clean.WriteString(rest[:start])
				changed = true
				rest = rest[clearEnd:]
				continue
			}
			clean.WriteString(rest[:clearEnd])
			rest = rest[clearEnd:]
			continue
		}
		clean.WriteString(rest[:start])
		replies = append(replies, parsed...)
		rest = rest[bodyEnd+len(close):]
		changed = true
	}
	if !changed {
		return text, nil
	}
	return strings.TrimSpace(clean.String()), replies
}

// dropEmptyFences removes a Markdown fence that only wrapped a tool marker.
// Fences around real content are preserved.
func dropEmptyFences(s string) string {
	t := strings.TrimSpace(s)
	if !strings.HasPrefix(t, "```") || !strings.HasSuffix(t, "```") || len(t) < 6 {
		return t
	}
	inner := t[3 : len(t)-3]
	if i := strings.IndexByte(inner, '\n'); i >= 0 {
		inner = inner[i+1:]
	}
	if hasVisibleText(inner) {
		return t
	}
	return ""
}

// extractFencedToolCalls accepts a tool call inside a Markdown fence anywhere
// in the message. Fences that do not contain one of the caller's tools are
// left untouched.
func extractFencedToolCalls(text string, allowed map[string]bool) (string, []ToolCallReply) {
	var clean strings.Builder
	var replies []ToolCallReply
	rest := text
	for {
		start := strings.Index(rest, "```")
		if start < 0 {
			clean.WriteString(rest)
			break
		}
		afterOpen := start + 3
		nl := strings.IndexByte(rest[afterOpen:], '\n')
		if nl < 0 {
			clean.WriteString(rest)
			break
		}
		bodyStart := afterOpen + nl + 1
		relEnd := strings.Index(rest[bodyStart:], "```")
		if relEnd < 0 {
			clean.WriteString(rest)
			break
		}
		bodyEnd := bodyStart + relEnd
		parsed := repliesFromEnvelopes(parseToolEnvelopes(strings.TrimSpace(rest[bodyStart:bodyEnd])), allowed)
		if len(parsed) == 0 {
			clean.WriteString(rest[:bodyEnd+3])
			rest = rest[bodyEnd+3:]
			continue
		}
		clean.WriteString(rest[:start])
		replies = append(replies, parsed...)
		rest = rest[bodyEnd+3:]
	}
	if len(replies) == 0 {
		return text, nil
	}
	return dropEmptyFences(strings.TrimSpace(clean.String())), replies
}

// extractBareToolCall accepts a whole-message JSON tool call, optionally
// wrapped in a Markdown fence.
func extractBareToolCall(text string, allowed map[string]bool) (string, []ToolCallReply) {
	trimmed := strings.TrimSpace(text)
	if strings.HasPrefix(trimmed, "```") {
		if i := strings.Index(trimmed, "\n"); i >= 0 {
			trimmed = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(trimmed[i+1:]), "```"))
		}
	}
	if !strings.HasPrefix(trimmed, "{") && !strings.HasPrefix(trimmed, "[") {
		return text, nil
	}
	replies := repliesFromEnvelopes(parseToolEnvelopes(trimmed), allowed)
	if len(replies) == 0 {
		return text, nil
	}
	return "", replies
}

func parseClientToolCalls(text string, allowed map[string]bool) (string, []ToolCallReply) {
	clean, replies := extractTaggedToolCalls(text, allowed)
	if len(replies) > 0 || clean != text {
		return dropEmptyFences(clean), replies
	}
	clean, replies = extractFencedToolCalls(text, allowed)
	if len(replies) > 0 {
		return clean, replies
	}
	return extractBareToolCall(text, allowed)
}

// limitToolReplies enforces tool_choice/parallel_tool_calls limits.
func limitToolReplies(replies []ToolCallReply, mode, name string, parallel bool) []ToolCallReply {
	out := make([]ToolCallReply, 0, len(replies))
	for _, reply := range replies {
		if mode == "function" && name != "" && reply.Name != name {
			continue
		}
		out = append(out, reply)
		if !parallel {
			break
		}
	}
	return out
}

func replyToolCalls(replies []ToolCallReply) []ToolCall {
	out := make([]ToolCall, 0, len(replies))
	for _, r := range replies {
		out = append(out, ToolCall{
			ID:   r.ID,
			Type: "function",
			Function: FunctionCall{
				Name:      r.Name,
				Arguments: r.Arguments,
			},
		})
	}
	return out
}

// nativeToolReplies maps upstream-native tool events to client tool calls.
// Only names the caller declared are exposed.
func nativeToolReplies(calls []ToolCallState, allowed map[string]bool) []ToolCallReply {
	out := make([]ToolCallReply, 0, len(calls))
	for _, call := range calls {
		name := resolveToolName(call.Name, allowed)
		if name == "" {
			continue
		}
		id := strings.TrimSpace(call.ID)
		if id == "" {
			id = "call_" + strings.ReplaceAll(uuid(), "-", "")
		}
		args := strings.TrimSpace(call.Arguments)
		if args == "" || !json.Valid([]byte(args)) {
			args = "{}"
		}
		out = append(out, ToolCallReply{ID: id, Name: name, Arguments: args})
	}
	return out
}

func requestedToolNames(req *ChatRequest) map[string]bool {
	names := make(map[string]bool)
	for _, t := range req.Tools {
		if name := strings.TrimSpace(t.Function.Name); name != "" {
			names[name] = true
		}
	}
	for _, f := range req.Functions {
		if name := strings.TrimSpace(f.Name); name != "" {
			names[name] = true
		}
	}
	return names
}

func parseToolChoice(v any) (mode, name string) {
	switch x := v.(type) {
	case string:
		return strings.ToLower(strings.TrimSpace(x)), ""
	case map[string]any:
		if fn, ok := x["function"].(map[string]any); ok {
			if n, ok := fn["name"].(string); ok {
				return "function", strings.TrimSpace(n)
			}
		}
		if n, ok := x["name"].(string); ok {
			return "function", strings.TrimSpace(n)
		}
		if t, ok := x["type"].(string); ok {
			return strings.ToLower(strings.TrimSpace(t)), ""
		}
	}
	return "auto", ""
}
