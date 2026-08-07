package fsm

import "testing"

// auditOnlyStage devolve uma etapa que só produz relatório de auditoria — o formato de
// `qa` no fluxo padrão. Fake nomeado em vez de literal inline: o mesmo contrato
// é exercitado por vários cenários, e nomeá-lo diz por que ele é assim.
func auditOnlyStage() Stage {
	return Stage{
		ID:               "qa",
		Role:             "qa",
		Requires:         []Artifact{"ci_green", "briefing"},
		Produces:         nil,
		ProducesForHuman: []Artifact{"qa_report"},
	}
}

// TestStageSeparatesTheThreeFields cobre o cenário A1.
//
// Uma etapa declara três coisas distintas: o que exige para começar, o que
// entrega ao fluxo e o que entrega a uma pessoa. Os três são campos separados —
// fundi-los apagaria a diferença entre "produto que alguém consome" e "relatório
// que alguém lê", que é o que ADR-0021 existe para preservar.
func TestStageSeparatesTheThreeFields(t *testing.T) {
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

// TestAuditArtifactIsNotAFlowProduct cobre o cenário A2.
//
// Um relatório declarado em ProducesForHuman não aparece entre os produtos que o
// fluxo consome. É o que impede um artefato de auditoria de satisfazer, por
// engano, o Requires de outra etapa.
func TestAuditArtifactIsNotAFlowProduct(t *testing.T) {
	qa := auditOnlyStage()

	if qa.ProducesArtifact("qa_report") {
		t.Error("qa_report é artefato de auditoria e não deveria contar como produto do fluxo")
	}
	if len(qa.ProducesForHuman) != 1 || qa.ProducesForHuman[0] != "qa_report" {
		t.Errorf("ProducesForHuman: quis [qa_report], veio %v", qa.ProducesForHuman)
	}
}

// TestStageMayProduceNothingForTheFlow cobre o cenário A3.
//
// A etapa `qa` entrega apenas um relatório de auditoria. Isso é declaração
// legítima, não etapa malformada: o valor dela é o parecer que uma pessoa lê,
// não um artefato que o fluxo encadeia.
func TestStageMayProduceNothingForTheFlow(t *testing.T) {
	qa := auditOnlyStage()

	if len(qa.Produces) != 0 {
		t.Errorf("Produces: quis vazio para uma etapa só de auditoria, veio %v", qa.Produces)
	}
	if len(qa.ProducesForHuman) == 0 {
		t.Error("uma etapa que não produz para o fluxo precisa produzir para alguém")
	}
}

// TestConditionalStageAppliesByKind cobre a base dos cenários C1 e C2.
//
// Uma etapa com condição entra no fluxo para umas naturezas de tarefa e não para
// outras. `spec` entra em feature e bug; é pulada em chore e docs.
func TestConditionalStageAppliesByKind(t *testing.T) {
	spec := Stage{
		ID:       "spec",
		Requires: []Artifact{"approach"},
		Produces: []Artifact{"contract"},
		When:     func(c TaskContext) bool { return c.Kind == KindFeature || c.Kind == KindBug },
	}

	cases := []struct {
		kind TaskKind
		want bool
	}{
		{KindFeature, true},
		{KindBug, true},
		{KindChore, false},
		{KindDocs, false},
	}

	for _, c := range cases {
		if got := spec.AppliesTo(NewTaskContext(c.kind)); got != c.want {
			t.Errorf("spec.AppliesTo(%q): quis %v, veio %v", c.kind, c.want, got)
		}
	}
}

// TestUnconditionalStageAlwaysApplies cobre a borda de AppliesTo.
//
// Uma etapa sem condição declarada entra para qualquer natureza de tarefa. É o
// caso da maioria: só as etapas de revisão pesada e a `spec` são condicionais.
func TestUnconditionalStageAlwaysApplies(t *testing.T) {
	setup := Stage{ID: "setup", Requires: []Artifact{"repos"}, Produces: []Artifact{"worktree"}}

	for _, k := range []TaskKind{KindFeature, KindBug, KindChore, KindDocs} {
		if !setup.AppliesTo(NewTaskContext(k)) {
			t.Errorf("setup.AppliesTo(%q): etapa sem condição deve entrar sempre", k)
		}
	}
}

// TestStageConditionedOnDiscoveredFact cobre a condição que não é natureza da tarefa.
//
// `architecture` só entra quando a mudança tocou a estrutura — fato que ninguém
// sabe no intake, e que só aparece depois de olhar o que o build produziu. É o
// caso que fez `When` receber o contexto inteiro em vez de só o kind.
func TestStageConditionedOnDiscoveredFact(t *testing.T) {
	arch := Stage{
		ID:               "architecture",
		Requires:         []Artifact{"code"},
		ProducesForHuman: []Artifact{"arch_report"},
		When:             func(c TaskContext) bool { return c.HasFact(TouchesStructure) },
	}

	ctx := NewTaskContext(KindFeature)
	if arch.AppliesTo(ctx) {
		t.Error("sem o fato descoberto, architecture não deveria entrar")
	}

	ctx.Facts[TouchesStructure] = true
	if !arch.AppliesTo(ctx) {
		t.Error("com o fato descoberto, architecture deveria entrar")
	}
}

// TestTaskContextHandlesNilMaps cobre a borda de um contexto montado à mão.
//
// Uma condição não deveria precisar saber se alguém inicializou os mapas antes
// de consultá-los — leitura de mapa nil em Go devolve o zero, e é isso que se
// espera aqui.
func TestTaskContextHandlesNilMaps(t *testing.T) {
	var ctx TaskContext

	if ctx.HasFact(TouchesStructure) {
		t.Error("contexto vazio não tem fato descoberto")
	}
	if ctx.HasArtifact(TaskID) {
		t.Error("contexto vazio não tem artefato")
	}
}
