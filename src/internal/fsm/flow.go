package fsm

// isFeatureOrBug é a condição que governa as etapas cujo custo só se paga quando
// há comportamento novo ou defeito a corrigir.
func isFeatureOrBug(k TaskKind) bool { return k == KindFeature || k == KindBug }

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
			When:             func(k TaskKind) bool { return k == KindBug },
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
			ID:       "build",
			Role:     "implementer",
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
			When:             func(k TaskKind) bool { return k != KindChore },
		},
		{
			ID:               "code-review",
			Role:             "reviewer",
			Requires:         []Artifact{"code", "ci_green"},
			ProducesForHuman: []Artifact{"review_report"},
			When:             func(k TaskKind) bool { return k != KindDocs },
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
			// A condição de "mexe em estrutura" não é a natureza da tarefa, e
			// sim um fato descoberto durante a execução. Fica fora de When —
			// que só recebe TaskKind — até o contexto de execução existir.
		},
		{
			ID:       "commit",
			Requires: []Artifact{"ci_green", "code"},
			Produces: []Artifact{"commit_sha"},
		},
	}
}
