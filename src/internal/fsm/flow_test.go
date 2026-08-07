package fsm

import "testing"

// TestFluxoPadraoEIntegro é o teste que mais importa deste pacote.
//
// Se o fluxo que a Luna traz instalado tiver uma exigência sem produtor, toda
// tarefa que o percorrer vai travar — e travar na etapa errada, com o sintoma
// deslocado da causa. Este teste pega isso no CI, antes de qualquer execução.
func TestFluxoPadraoEIntegro(t *testing.T) {
	gaps := AuditContract(DefaultFlow())

	if len(gaps) != 0 {
		for _, g := range gaps {
			t.Errorf("etapa %q exige %v, que nenhuma etapa anterior produz", g.Stage, g.Missing)
		}
	}
}

// TestFluxoPadraoTemAsQuatorzeEtapas guarda a tabela de docs/architecture/stages.md.
//
// Não é contagem por contagem: uma etapa que suma de DefaultFlow sem sumir da
// documentação deixa os dois divergentes, e a documentação é o contrato.
func TestFluxoPadraoTemAsQuatorzeEtapas(t *testing.T) {
	flow := DefaultFlow()

	if len(flow) != 14 {
		t.Errorf("quis 14 etapas conforme docs/architecture/stages.md, veio %d", len(flow))
	}

	quer := []StageID{
		"discovery", "setup", "intake", "diagnose", "scenarios", "spec", "build",
		"refactor", "verify", "qa", "code-review", "harden", "architecture", "commit",
	}
	for i, id := range quer {
		if i >= len(flow) {
			t.Fatalf("fluxo terminou antes de %q", id)
		}
		if flow[i].ID != id {
			t.Errorf("posição %d: quis %q, veio %q", i, id, flow[i].ID)
		}
	}
}

// TestRelatoriosDeAuditoriaNaoSaoProdutosDoFluxo cobre INV-core-11 no fluxo real.
//
// Os pareceres de qa, code-review, harden e architecture existem para uma pessoa
// ler. Se algum deles virasse Produces, passaria a satisfazer Requires de outra
// etapa e a distinção de ADR-0021 perderia o sentido no fluxo que mais importa.
func TestRelatoriosDeAuditoriaNaoSaoProdutosDoFluxo(t *testing.T) {
	relatorios := map[StageID]Artifact{
		"qa":           "qa_report",
		"code-review":  "review_report",
		"harden":       "mutation_report",
		"architecture": "arch_report",
		"verify":       "dod_checked",
		"diagnose":     "min_case",
	}

	for _, stage := range DefaultFlow() {
		report, temRelatorio := relatorios[stage.ID]
		if !temRelatorio {
			continue
		}
		if stage.ProducesArtifact(report) {
			t.Errorf("%q: %q é artefato de auditoria e não deveria estar em Produces", stage.ID, report)
		}
		if !contains(stage.ProducesForHuman, report) {
			t.Errorf("%q: quis %q em ProducesForHuman, veio %v", stage.ID, report, stage.ProducesForHuman)
		}
	}
}

// TestEtapasCondicionaisDoFluxoPadrao guarda a coluna "Condição" da tabela.
//
// Rodar teste de mutação num chore de uma linha é a cerimônia que ADR-0014
// existe para cortar. Se uma condição se perder, o fluxo passa a rodar etapa
// cara onde ela não se paga — e ninguém nota, porque o resultado continua certo.
func TestEtapasCondicionaisDoFluxoPadrao(t *testing.T) {
	casos := []struct {
		stage StageID
		kind  TaskKind
		entra bool
	}{
		{"diagnose", KindBug, true},
		{"diagnose", KindFeature, false},
		{"spec", KindFeature, true},
		{"spec", KindChore, false},
		{"qa", KindChore, false},
		{"qa", KindFeature, true},
		{"code-review", KindDocs, false},
		{"code-review", KindFeature, true},
		{"harden", KindBug, true},
		{"harden", KindDocs, false},
		{"build", KindDocs, true},
	}

	byID := map[StageID]Stage{}
	for _, s := range DefaultFlow() {
		byID[s.ID] = s
	}

	for _, c := range casos {
		stage, ok := byID[c.stage]
		if !ok {
			t.Fatalf("etapa %q não existe no fluxo padrão", c.stage)
		}
		if got := stage.AppliesTo(c.kind); got != c.entra {
			t.Errorf("%q com kind=%q: quis entra=%v, veio %v", c.stage, c.kind, c.entra, got)
		}
	}
}

func contains(list []Artifact, want Artifact) bool {
	for _, a := range list {
		if a == want {
			return true
		}
	}
	return false
}
