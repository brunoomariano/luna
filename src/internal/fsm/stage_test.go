package fsm

import "testing"

// stageQA devolve uma etapa que só produz relatório de auditoria — o formato de
// `qa` no fluxo padrão. Fake nomeado em vez de literal inline: o mesmo contrato
// é exercitado por vários cenários, e nomeá-lo diz por que ele é assim.
func stageQA() Stage {
	return Stage{
		ID:               "qa",
		Role:             "qa",
		Requires:         []Artifact{"ci_green", "briefing"},
		Produces:         nil,
		ProducesForHuman: []Artifact{"qa_report"},
	}
}

// TestStageSeparaOsTresCampos cobre o cenário A1.
//
// Uma etapa declara três coisas distintas: o que exige para começar, o que
// entrega ao fluxo e o que entrega a uma pessoa. Os três são campos separados —
// fundi-los apagaria a diferença entre "produto que alguém consome" e "relatório
// que alguém lê", que é o que ADR-0021 existe para preservar.
func TestStageSeparaOsTresCampos(t *testing.T) {
	build := Stage{
		ID:       "build",
		Requires: []Artifact{"scenarios", "approach", "worktree"},
		Produces: []Artifact{"code", "tests_green"},
	}

	if len(build.Requires) != 3 {
		t.Errorf("Requires: quis 3 artefatos, veio %d (%v)", len(build.Requires), build.Requires)
	}
	if len(build.Produces) != 2 {
		t.Errorf("Produces: quis 2 artefatos, veio %d (%v)", len(build.Produces), build.Produces)
	}
	if len(build.ProducesForHuman) != 0 {
		t.Errorf("ProducesForHuman: quis vazio, veio %v", build.ProducesForHuman)
	}
}

// TestArtefatoDeAuditoriaNaoContaComoProdutoDoFluxo cobre o cenário A2.
//
// Um relatório declarado em ProducesForHuman não aparece entre os produtos que o
// fluxo consome. É o que impede um artefato de auditoria de satisfazer, por
// engano, o Requires de outra etapa.
func TestArtefatoDeAuditoriaNaoContaComoProdutoDoFluxo(t *testing.T) {
	qa := stageQA()

	if qa.ProducesArtifact("qa_report") {
		t.Error("qa_report é artefato de auditoria e não deveria contar como produto do fluxo")
	}
	if len(qa.ProducesForHuman) != 1 || qa.ProducesForHuman[0] != "qa_report" {
		t.Errorf("ProducesForHuman: quis [qa_report], veio %v", qa.ProducesForHuman)
	}
}

// TestEtapaPodeNaoProduzirNadaParaOFluxo cobre o cenário A3.
//
// A etapa `qa` entrega apenas um relatório de auditoria. Isso é declaração
// legítima, não etapa malformada: o valor dela é o parecer que uma pessoa lê,
// não um artefato que o fluxo encadeia.
func TestEtapaPodeNaoProduzirNadaParaOFluxo(t *testing.T) {
	qa := stageQA()

	if len(qa.Produces) != 0 {
		t.Errorf("Produces: quis vazio para uma etapa só de auditoria, veio %v", qa.Produces)
	}
	if len(qa.ProducesForHuman) == 0 {
		t.Error("uma etapa que não produz para o fluxo precisa produzir para alguém")
	}
}

// TestEtapaCondicionalEntraConformeOKind cobre a base dos cenários C1 e C2.
//
// Uma etapa com condição entra no fluxo para umas naturezas de tarefa e não para
// outras. `spec` entra em feature e bug; é pulada em chore e docs.
func TestEtapaCondicionalEntraConformeOKind(t *testing.T) {
	spec := Stage{
		ID:       "spec",
		Requires: []Artifact{"approach"},
		Produces: []Artifact{"contract"},
		When:     func(k TaskKind) bool { return k == KindFeature || k == KindBug },
	}

	casos := []struct {
		kind TaskKind
		quer bool
	}{
		{KindFeature, true},
		{KindBug, true},
		{KindChore, false},
		{KindDocs, false},
	}

	for _, c := range casos {
		if got := spec.AppliesTo(c.kind); got != c.quer {
			t.Errorf("spec.AppliesTo(%q): quis %v, veio %v", c.kind, c.quer, got)
		}
	}
}

// TestEtapaSemCondicaoEntraSempre cobre a borda de AppliesTo.
//
// Uma etapa sem condição declarada entra para qualquer natureza de tarefa. É o
// caso da maioria: só as etapas de revisão pesada e a `spec` são condicionais.
func TestEtapaSemCondicaoEntraSempre(t *testing.T) {
	setup := Stage{ID: "setup", Requires: []Artifact{"repos"}, Produces: []Artifact{"worktree"}}

	for _, k := range []TaskKind{KindFeature, KindBug, KindChore, KindDocs} {
		if !setup.AppliesTo(k) {
			t.Errorf("setup.AppliesTo(%q): etapa sem condição deve entrar sempre", k)
		}
	}
}
