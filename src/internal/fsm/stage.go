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

// Fact é algo descoberto sobre a tarefa **durante** a execução — diferente de
// TaskKind, que ela carrega desde o intake. É o que permite a uma etapa
// condicional depender do que o trabalho revelou, não só do que se sabia antes
// de começar.
type Fact string

// TouchesStructure marca que a mudança mexeu na estrutura do sistema. Só se sabe
// olhando o que o build produziu, e é a condição de entrada de `architecture`.
const TouchesStructure Fact = "touches_structure"

// TaskContext é o que uma condição de etapa consulta para decidir se entra no
// fluxo: a natureza da tarefa, os artefatos já produzidos e os fatos descobertos
// ao longo da execução.
//
// Existe um tipo em vez de passar TaskKind solto porque nem toda condição é
// natureza da tarefa. `diagnose` depende do kind, sabido no intake;
// `architecture` depende de a mudança ter tocado a estrutura, o que só se sabe
// depois do build. Um único parâmetro serve as duas sem duplicar o mecanismo.
type TaskContext struct {
	Kind      TaskKind
	Artifacts map[Artifact]bool
	Facts     map[Fact]bool
}

// NewTaskContext monta um contexto para uma tarefa que está começando: só o
// kind, sem artefato produzido nem fato descoberto.
func NewTaskContext(kind TaskKind) TaskContext {
	return TaskContext{
		Kind:      kind,
		Artifacts: map[Artifact]bool{TaskID: true},
		Facts:     map[Fact]bool{},
	}
}

// HasFact informa se o fato foi descoberto. Consulta segura em contexto de mapa
// nil — uma condição não deveria precisar saber se alguém inicializou o mapa.
func (c TaskContext) HasFact(f Fact) bool {
	return c.Facts[f]
}

// HasArtifact informa se o artefato já está disponível no contexto.
func (c TaskContext) HasArtifact(a Artifact) bool {
	return c.Artifacts[a]
}

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
	//
	// Recebe o contexto inteiro, não só o kind: nem toda condição é natureza da
	// tarefa. `diagnose` olha o kind; `architecture` olha um fato descoberto
	// durante a execução.
	When func(TaskContext) bool
}

// ProducesArtifact informa se a etapa entrega o artefato para o fluxo consumir.
// ProducesForHuman não conta: um relatório de auditoria não satisfaz o Requires
// de ninguém (INV-core-11).
func (s Stage) ProducesArtifact(a Artifact) bool {
	return containsArtifact(s.Produces, a)
}

// ProducesForHumanArtifact informa se a etapa entrega o artefato para leitura
// humana. Separado de ProducesArtifact porque a pergunta é outra: aqui não se
// checa disponibilidade para o fluxo, e sim se a etapa se comprometeu a entregar
// um parecer.
func (s Stage) ProducesForHumanArtifact(a Artifact) bool {
	return containsArtifact(s.ProducesForHuman, a)
}

// containsArtifact é busca linear porque as listas de um contrato de etapa têm
// meia dúzia de itens — um índice custaria mais em alocação do que economiza em
// comparação.
func containsArtifact(list []Artifact, want Artifact) bool {
	for _, a := range list {
		if a == want {
			return true
		}
	}
	return false
}

// AppliesTo informa se a etapa entra no fluxo neste contexto.
func (s Stage) AppliesTo(ctx TaskContext) bool {
	if s.When == nil {
		return true
	}
	return s.When(ctx)
}
