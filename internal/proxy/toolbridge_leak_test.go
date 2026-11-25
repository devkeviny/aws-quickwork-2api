package proxy

import (
	"strings"
	"testing"
)

// Reproduz os 2 blocos que vazaram no Telegram em 24/nov 21:13 e 21:49.
func TestLeakBlocks(t *testing.T) {
	allowed := map[string]bool{"terminal": true, "update_task_status": true, "read_file": true}

	block1 := `<client_tool_call>{"name":"terminal","arguments":{"command":"/opt/hermes/.venv/bin/hermes -p platform sessions --help 2>&1 | head -40; echo ---; /opt/hermes/.venv/bin/hermes -p platform sessions export local-623a6f8843de4ae962b5003e2a1198a5ba6469f28f71f41e150eddcce025a5e8 2>&1 | tail -c 6000","timeout":60}}</client_tool_call>`
	clean1, replies1 := parseClientToolCalls(block1, allowed)
	t.Logf("bloco1: replies=%d clean=%q", len(replies1), clean1)
	if len(replies1) != 1 {
		t.Errorf("bloco1: esperava 1 reply, veio %d (vazou)", len(replies1))
	}

	block2 := `<client_tool_call>{"name":"tool_call","arguments":{"calls":[{"name":"update_task_status","arguments":{"mission_id":"regras-globais-2026-11-24","role":"Platform","status":"DONE","sage_note":"x","force_now":true}},{"name":"update_task_status","arguments":{"mission_id":"regras-globais-2026-11-24","role":"Coder","status":"BLOCKED","task":"y","force_now":true}}]}}</client_tool_call>`
	clean2, replies2 := parseClientToolCalls(block2, allowed)
	t.Logf("bloco2: replies=%d clean=%q", len(replies2), clean2)
	if len(replies2) != 2 {
		t.Errorf("bloco2: esperava 2 replies, veio %d (vazou)", len(replies2))
	}
}

func TestLeakBlocksNestedToolCallsVariant(t *testing.T) {
	allowed := map[string]bool{"terminal": true}
	block := `<client_tool_call>{"name":"tool_call","arguments":{"tool_calls":[{"name":"terminal","arguments":{"command":"echo hi"}}]}}</client_tool_call>`
	_, replies := parseClientToolCalls(block, allowed)
	if len(replies) != 1 || replies[0].Name != "terminal" {
		t.Errorf("variante tool_calls aninhada: esperava 1 reply terminal, veio %+v", replies)
	}
}

func TestLeakBlocksRealToolWithCallsArgument(t *testing.T) {
	// Uma ferramenta real cujo argumento se chama "calls" nunca é confundida
	// com um lote: o nome "scheduler" não é um wrapper genérico.
	allowed := map[string]bool{"scheduler": true}
	block := `<client_tool_call>{"name":"scheduler","arguments":{"calls":["a","b"]}}</client_tool_call>`
	_, replies := parseClientToolCalls(block, allowed)
	if len(replies) != 1 || replies[0].Name != "scheduler" {
		t.Errorf("ferramenta real com arg calls: esperava 1 reply scheduler, veio %+v", replies)
	}
}

func TestLeakBlocksUnparsedJSONDropped(t *testing.T) {
	// Bloco JSON válido para ferramenta DESCONHECIDA (fora da lista):
	// não vaza no texto — é removido, pois é protocolo de máquina.
	allowed := map[string]bool{"terminal": true}
	block := `Antes <client_tool_call>{"name":"ferramenta_inexistente","arguments":{"x":1}}</client_tool_call> depois`
	clean, replies := parseClientToolCalls(block, allowed)
	if len(replies) != 0 {
		t.Errorf("bloco desconhecido: esperava 0 replies, veio %d", len(replies))
	}
	if len(clean) == 0 || clean == block {
		t.Errorf("bloco desconhecido não foi removido do texto: %q", clean)
	}
	for _, bad := range []string{"client_tool_call", "ferramenta_inexistente"} {
		if strings.Contains(clean, bad) {
			t.Errorf("bloco desconhecido vazou no texto (%q): %q", bad, clean)
		}
	}
	if !strings.Contains(clean, "Antes") || !strings.Contains(clean, "depois") {
		t.Errorf("texto ao redor do bloco foi perdido: %q", clean)
	}
}

func TestLeakBlocksNonJSONKept(t *testing.T) {
	// Corpo que não é JSON pode ser texto comum — nunca é apagado.
	allowed := map[string]bool{"terminal": true}
	block := `<client_tool_call>isso aqui é só um texto</client_tool_call>`
	clean, replies := parseClientToolCalls(block, allowed)
	if len(replies) != 0 {
		t.Errorf("bloco não-JSON: esperava 0 replies, veio %d", len(replies))
	}
	if !strings.Contains(clean, "isso aqui é só um texto") {
		t.Errorf("bloco não-JSON foi apagado: %q", clean)
	}
}

// Os blocos que vazaram em 24/nov 22:35 e 22:43 vieram de turnos SEM
// tools (bridge desligado): a limpeza antiga só rodava com o bridge.
// Com allowed vazio, o bloco some do texto do mesmo jeito.
func TestLeakBlocksNoTools(t *testing.T) {
	allowed := map[string]bool{}

	block2243 := `<client_tool_call>{"name":"tool_call","arguments":{"calls":[{"name":"update_task_status","arguments":{"mission_id":"regras-globais-2026-11-24","role":"Coder","status":"DONE","task":"AGENTS.md criado e enviado (fc35ffc em origin/main)","force_now":true},{"name":"update_task_status","arguments":{"mission_id":"regras-globais-2026-11-24","role":"Platform","status":"BLOCKED","task":"Limpeza de worktrees aguardando aprovação do Kevinny; painel em teste","force_now":true}]}}</client_tool_call>`
	clean, replies := parseClientToolCalls(block2243, allowed)
	if len(replies) != 0 {
		t.Errorf("sem tools: esperava 0 replies, veio %d", len(replies))
	}
	if strings.Contains(clean, "client_tool_call") || strings.Contains(clean, "update_task_status") {
		t.Errorf("bloco 22:43 vazou sem tools: %q", clean)
	}
	if clean != "" {
		t.Errorf("bloco 22:43 sozinho deveria sumir, sobrou: %q", clean)
	}

	prose := `The latest part of the file probably holds the tool result. I'll read the end.` + block2243
	clean2, _ := parseClientToolCalls(prose, allowed)
	if strings.Contains(clean2, "client_tool_call") {
		t.Errorf("bloco 22:35 vazou sem tools: %q", clean2)
	}
	if !strings.Contains(clean2, "The latest part") {
		t.Errorf("prosa ao redor do bloco foi perdida: %q", clean2)
	}
}

func TestLeakBlocksUnclosedTagDropped(t *testing.T) {
	// Bloco truncado (sem a tag de fechamento) não vaza: some com o resto.
	allowed := map[string]bool{"terminal": true}
	text := `Antes <client_tool_call>{"name":"terminal","arguments":{"command":"echo`
	clean, _ := parseClientToolCalls(text, allowed)
	if strings.Contains(clean, "client_tool_call") {
		t.Errorf("tag sem fechamento vazou: %q", clean)
	}
	if !strings.Contains(clean, "Antes") {
		t.Errorf("prosa antes da tag foi perdida: %q", clean)
	}
}

func TestStreamScrubberSplitBlock(t *testing.T) {
	// O texto do 22:35 chegando em 3 pedaços, com o bloco partido no meio
	// da tag, do JSON e do fechamento.
	allowed := map[string]bool{}
	chunks := []string{
		`The latest part of the file probably holds the tool result. I'll read the end.<client_to`,
		`ol_call>{"name":"tool_call","arguments":{"calls":[{"name":"update_task_status","arguments":{"mission_id":"regras-globais-2026-11-24","role":"Coder","status":"DONE"`,
		`,"task":"AGENTS.md criado e enviado (fc35ffc em origin/main)","force_now":true}}]}}</client_tool_call> FIM.`,
	}
	s := newStreamScrubber(allowed)
	var out strings.Builder
	for _, c := range chunks {
		out.WriteString(s.push(c))
	}
	out.WriteString(s.flush())
	got := out.String()
	if strings.Contains(got, "client_tool_call") || strings.Contains(got, "update_task_status") {
		t.Errorf("bloco partido vazou no stream: %q", got)
	}
	if !strings.Contains(got, "The latest part") || !strings.Contains(got, "FIM.") {
		t.Errorf("prosa ao redor do bloco foi perdida: %q", got)
	}
}

func TestStreamScrubberBlockOnly(t *testing.T) {
	// Resposta que é SÓ o bloco (caso 22:43): nada sai.
	allowed := map[string]bool{}
	block := `<client_tool_call>{"name":"tool_call","arguments":{"calls":[{"name":"update_task_status","arguments":{"mission_id":"regras-globais-2026-11-24","role":"Platform","status":"BLOCKED","force_now":true}}]}}</client_tool_call>`
	s := newStreamScrubber(allowed)
	var out strings.Builder
	mid := len(block) / 2
	out.WriteString(s.push(block[:mid]))
	out.WriteString(s.push(block[mid:]))
	out.WriteString(s.flush())
	if got := out.String(); got != "" {
		t.Errorf("bloco sozinho deveria sumir, saiu: %q", got)
	}
}

func TestStreamScrubberAngleBracket(t *testing.T) {
	// "<" comum em prosa passa intacto.
	allowed := map[string]bool{}
	s := newStreamScrubber(allowed)
	var out strings.Builder
	out.WriteString(s.push("a < b and 5 < 10, x <"))
	out.WriteString(s.push(" y, fim."))
	out.WriteString(s.flush())
	if got := out.String(); got != "a < b and 5 < 10, x < y, fim." {
		t.Errorf("prosa com < foi alterada: %q", got)
	}
}
