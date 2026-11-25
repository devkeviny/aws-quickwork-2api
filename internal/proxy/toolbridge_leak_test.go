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
