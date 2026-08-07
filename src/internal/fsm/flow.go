package fsm

// isFeatureOrBug é a condição que governa as etapas cujo custo só se paga quando
// há comportamento novo ou defeito a corrigir.
func isFeatureOrBug(c TaskContext) bool { return c.Kind == KindFeature || c.Kind == KindBug }

// DefaultFlow é o fluxo que a Luna traz instalado — as 14 etapas de
// docs/architecture/stages.md.
//
// Não é obrigatório: etapas podem ser desabilitadas, editadas ou substituídas, e
// novas podem ser criadas (ADR-0017). O que não muda é o contrato — toda etapa
// declara o que exige e o que produz.
//
// A ordem é significativa: AuditContract verifica precedência, não existência.
func DefaultFlow() []Stage {
	return []Stage{
		{
			ID:       "discovery",
			Requires: []Artifact{TaskID},
			Produces: []Artifact{"repos"},
		},
		{
			ID:       "setup",
			Requires: []Artifact{"repos"},
			Produces: []Artifact{"worktree"},
		},
		{
			ID:       "intake",
			Role:     "analyst",
			Requires: []Artifact{TaskID, "worktree"},
			Produces: []Artifact{"briefing", "kind"},
		},
		{
			ID:               "diagnose",
			Requires:         []Artifact{"briefing"},
			Produces:         []Artifact{"root_cause"},
			ProducesForHuman: []Artifact{"min_case"},
			When:             func(c TaskContext) bool { return c.Kind == KindBug },
		},
		{
			ID:       "scenarios",
			Role:     "gherkin",
			Requires: []Artifact{"briefing", "kind"},
			Produces: []Artifact{"scenarios", "approach"},
		},
		{
			ID:       "spec",
			Requires: []Artifact{"approach"},
			Produces: []Artifact{"contract"},
			When:     isFeatureOrBug,
		},
		{
			ID:   "build",
			Role: "implementer",
			// ADR-0022 prevê `contract` aqui, exigido só quando `spec` entrou no
			// fluxo. Fica de fora até o mecanismo de requires condicional ser
			// escolhido — declará-lo sem esse mecanismo faria toda tarefa
			// `chore` ou `docs` travar, já que `spec` é pulada nelas.
			// Ver a nota em docs/architecture/stages.md.
			Requires: []Artifact{"scenarios", "approach", "worktree"},
			Produces: []Artifact{"code", "tests_green"},
		},
		{
			ID:       "refactor",
			Role:     "cleaner",
			Requires: []Artifact{"code", "tests_green"},
			Produces: []Artifact{"code"},
		},
		{
			ID:               "verify",
			Requires:         []Artifact{"code", "scenarios"},
			Produces:         []Artifact{"ci_green"},
			ProducesForHuman: []Artifact{"dod_checked"},
		},
		{
			ID:               "qa",
			Role:             "qa",
			Requires:         []Artifact{"ci_green", "briefing"},
			ProducesForHuman: []Artifact{"qa_report"},
			When:             func(c TaskContext) bool { return c.Kind != KindChore },
		},
		{
			ID:               "code-review",
			Role:             "reviewer",
			Requires:         []Artifact{"code", "ci_green"},
			ProducesForHuman: []Artifact{"review_report"},
			When:             func(c TaskContext) bool { return c.Kind != KindDocs },
		},
		{
			ID:               "harden",
			Role:             "hardener",
			Requires:         []Artifact{"tests_green", "code"},
			ProducesForHuman: []Artifact{"mutation_report"},
			When:             isFeatureOrBug,
		},
		{
			ID:               "architecture",
			Role:             "architect",
			Requires:         []Artifact{"code"},
			ProducesForHuman: []Artifact{"arch_report"},
			// Diferente das demais, esta condição não é natureza da tarefa: só
			// se sabe que a mudança mexeu na estrutura depois de olhar o que o
			// build produziu.
			When: func(c TaskContext) bool { return c.HasFact(TouchesStructure) },
		},
		{
			ID:       "commit",
			Requires: []Artifact{"ci_green", "code"},
			Produces: []Artifact{"commit_sha"},
		},
	}
}
