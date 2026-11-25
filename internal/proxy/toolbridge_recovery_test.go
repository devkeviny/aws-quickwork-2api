package proxy

import (
	"strings"
	"testing"
)

// Recuperação portada do magpie (httpdone/magpie-aws-quick, lib/tools.mjs):
// JSON cru, span {…} externo, ambos com controles escapados; mais a
// nossa extensão de completar até 3 '}' finais (os vazamentos de 24/nov).
func TestRecoveryLiteralNewlineInArguments(t *testing.T) {
	allowed := map[string]bool{"terminal": true}
	// Quebra de linha e tab LITERAIS dentro do valor da string — JSON
	// estrito rejeita, o magpie recupera (caso do heredoc de 6 KB).
	block := "<client_tool_call>{\"name\":\"terminal\",\"arguments\":{\"command\":\"echo line1\nline2\ttab\",\"timeout\":60}}</client_tool_call>"
	clean, replies := parseClientToolCalls(block, allowed)
	if len(replies) != 1 || replies[0].Name != "terminal" {
		t.Fatalf("newline literal: esperava 1 reply terminal, veio %+v (texto: %q)", replies, clean)
	}
	if !strings.Contains(replies[0].Arguments, `line1\nline2\ttab`) {
		t.Errorf("newline literal: argumentos perderam o conteúdo: %q", replies[0].Arguments)
	}
	if strings.Contains(clean, "client_tool_call") {
		t.Errorf("newline literal: bloco vazou no texto: %q", clean)
	}
}

func TestRecoveryStrayMarkupSpan(t *testing.T) {
	allowed := map[string]bool{"terminal": true}
	// O modelo embrulhou o JSON numa cerca ```json dentro da tag.
	block := "<client_tool_call>```json\n{\"name\":\"terminal\",\"arguments\":{\"command\":\"echo hi\"}}\n```</client_tool_call>"
	clean, replies := parseClientToolCalls(block, allowed)
	if len(replies) != 1 || replies[0].Name != "terminal" {
		t.Fatalf("cerca json: esperava 1 reply terminal, veio %+v (texto: %q)", replies, clean)
	}
	if strings.Contains(clean, "client_tool_call") || strings.Contains(clean, "```") {
		t.Errorf("cerca json: markup vazou no texto: %q", clean)
	}
}

func TestRecoveryMissingBrace(t *testing.T) {
	// O formato exato dos vazamentos de 24/nov: falta o '}' final.
	allowed := map[string]bool{"update_task_status": true}
	block := `<client_tool_call>{"name":"update_task_status","arguments":{"mission_id":"m1","status":"DONE","force_now":true}</client_tool_call>`
	clean, replies := parseClientToolCalls(block, allowed)
	if len(replies) != 1 || replies[0].Name != "update_task_status" {
		t.Fatalf("falta }: esperava 1 reply update_task_status, veio %+v (texto: %q)", replies, clean)
	}
	if !strings.Contains(replies[0].Arguments, `"status":"DONE"`) {
		t.Errorf("falta }: argumentos incompletos: %q", replies[0].Arguments)
	}
	if strings.Contains(clean, "client_tool_call") {
		t.Errorf("falta }: bloco vazou no texto: %q", clean)
	}
}

func TestRecoveryMissingBracePlusNewline(t *testing.T) {
	// Falta '}' E tem newline literal: só a combinação
	// escapar + completar recupera.
	allowed := map[string]bool{"terminal": true}
	block := "<client_tool_call>{\"name\":\"terminal\",\"arguments\":{\"command\":\"echo a\nb\",\"timeout\":60}</client_tool_call>"
	clean, replies := parseClientToolCalls(block, allowed)
	if len(replies) != 1 || replies[0].Name != "terminal" {
		t.Fatalf("falta } + newline: esperava 1 reply terminal, veio %+v (texto: %q)", replies, clean)
	}
	if !strings.Contains(replies[0].Arguments, `echo a\nb`) {
		t.Errorf("falta } + newline: argumentos incompletos: %q", replies[0].Arguments)
	}
}

func TestRecoveryUnrecoverableDropped(t *testing.T) {
	allowed := map[string]bool{"terminal": true}
	cases := []string{
		// Truncado no meio da string: string sem fechar, nada completa.
		`<client_tool_call>{"name":"terminal","arguments":{"command":"echo abc</client_tool_call>`,
		// Lixo que começa com '{' mas não é JSON.
		`<client_tool_call>{not json at all</client_tool_call>`,
		// Chaves demais (profundidade negativa).
		`<client_tool_call>{"name":"terminal"}}}</client_tool_call>`,
	}
	for i, block := range cases {
		clean, replies := parseClientToolCalls(block, allowed)
		if len(replies) != 0 {
			t.Errorf("caso %d: irrecuperável não deveria virar reply: %+v", i, replies)
		}
		if strings.Contains(clean, "client_tool_call") || strings.Contains(clean, "not json") {
			t.Errorf("caso %d: irrecuperável vazou no texto: %q", i, clean)
		}
	}
}

func TestRecoveryUnknownToolStillDropped(t *testing.T) {
	// Recupera o JSON, mas o nome não é uma ferramenta do chamador:
	// 0 replies e o bloco some (não vaza).
	allowed := map[string]bool{"terminal": true}
	block := `<client_tool_call>{"name":"ferramenta_inexistente","arguments":{"x":1}</client_tool_call>`
	clean, replies := parseClientToolCalls(block, allowed)
	if len(replies) != 0 {
		t.Errorf("desconhecida recuperada: esperava 0 replies, veio %d", len(replies))
	}
	if strings.Contains(clean, "ferramenta_inexistente") || strings.Contains(clean, "client_tool_call") {
		t.Errorf("desconhecida recuperada vazou: %q", clean)
	}
}

func TestRecoveryBareMessage(t *testing.T) {
	// Mensagem inteira é o JSON (sem tag), com newline literal.
	allowed := map[string]bool{"terminal": true}
	text := "{\"name\":\"terminal\",\"arguments\":{\"command\":\"echo a\nb\"}}"
	clean, replies := parseClientToolCalls(text, allowed)
	if len(replies) != 1 || replies[0].Name != "terminal" {
		t.Fatalf("bare + newline: esperava 1 reply terminal, veio %+v (texto: %q)", replies, clean)
	}
	if clean != "" {
		t.Errorf("bare + newline: texto deveria sumir, sobrou: %q", clean)
	}
}

func TestRecoveryStreamScrubber(t *testing.T) {
	// Bloco com newline literal partido em 2 chunks no stream.
	allowed := map[string]bool{"terminal": true}
	chunks := []string{
		"<client_tool_call>{\"name\":\"terminal\",\"arguments\":{\"command\":\"echo a\n",
		"b\",\"timeout\":60}}</client_tool_call> FIM.",
	}
	s := newStreamScrubber(allowed)
	var out strings.Builder
	for _, c := range chunks {
		out.WriteString(s.push(c))
	}
	out.WriteString(s.flush())
	got := out.String()
	if strings.Contains(got, "client_tool_call") || strings.Contains(got, "echo a") {
		t.Errorf("stream com newline literal vazou: %q", got)
	}
	if !strings.Contains(got, "FIM.") {
		t.Errorf("stream perdeu a prosa: %q", got)
	}
}
