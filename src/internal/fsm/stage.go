// Package fsm é o motor da Luna: etapas, contrato e transições.
//
// A FSM decide qual etapa vem agora; o modelo trabalha dentro dela. Ver
// docs/invariants/core.md (INV-core-1) e docs/ADRs/0001-flow-control-out-of-model.md.
package fsm

// Artifact identifica um produto do fluxo — o que uma etapa exige para começar
// ou entrega ao terminar. É string nomeada, e não string crua, para que o
// compilador separe "nome de artefato" de qualquer outro texto que ande junto.
type Artifact string

// StageID identifica uma etapa dentro de um fluxo.
type StageID string

// TaskKind é a natureza da tarefa. Governa quais etapas condicionais entram no
// fluxo — ver docs/ADRs/0014-conditional-stages.md.
type TaskKind string

const (
	KindFeature TaskKind = "feature"
	KindBug     TaskKind = "bug"
	KindChore   TaskKind = "chore"
	KindDocs    TaskKind = "docs"
)

// TaskID é a raiz do grafo de artefatos: o único insumo que nenhuma etapa
// produz, porque a tarefa já chega com ele.
const TaskID Artifact = "task_id"

// Stage é o contrato de uma etapa: o que ela exige para começar e o que entrega
// ao terminar.
//
// Produces e ProducesForHuman são campos distintos de propósito. O primeiro é
// consumido por alguma etapa adiante e entra na verificação estática; o segundo
// é lido por uma pessoa e é isento dela — não é defeito ninguém consumi-lo. Ver
// docs/ADRs/0021-produces-for-human-is-a-separate-contract-field.md.
type Stage struct {
	ID   StageID
	Role string

	// Requires é o que precisa estar no contexto para a etapa começar.
	Requires []Artifact

	// Produces é o que o fluxo consome. Verificado na saída e na estática.
	Produces []Artifact

	// ProducesForHuman é o que só uma pessoa lê: relatórios, pareceres,
	// diagnósticos. Verificado na saída como Produces, isento da estática.
	ProducesForHuman []Artifact

	// When decide se a etapa entra no fluxo. Nil significa incondicional.
	When func(TaskKind) bool
}

// Produces informa se a etapa entrega o artefato para o fluxo consumir.
// ProducesForHuman não conta: um relatório de auditoria não satisfaz o Requires
// de ninguém (INV-core-11).
func (s Stage) ProducesArtifact(a Artifact) bool {
	for _, p := range s.Produces {
		if p == a {
			return true
		}
	}
	return false
}

// AppliesTo informa se a etapa entra no fluxo para esta natureza de tarefa.
func (s Stage) AppliesTo(kind TaskKind) bool {
	if s.When == nil {
		return true
	}
	return s.When(kind)
}
