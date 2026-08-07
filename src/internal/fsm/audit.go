package fsm

// ContractGap é uma etapa que exige artefatos que nenhuma etapa anterior produz.
// Carrega a etapa e o que falta: uma mensagem que diz só "contrato quebrado"
// custa uma sessão de depuração.
type ContractGap struct {
	Stage   StageID
	Missing []Artifact
}

// AuditContract percorre o fluxo em ordem e reporta toda etapa cujo Requires não
// é satisfeito por alguma etapa anterior.
//
// É a primeira das três verificações do contrato (INV-core-3), e a única que roda
// sem executar nada: detecta fluxo quebrado *no papel*, antes de qualquer agente
// ser chamado. As outras duas — de entrada e de saída — só podem falhar com a
// tarefa já rodando.
//
// ProducesForHuman deliberadamente não entra no conjunto disponível: um relatório
// de auditoria é lido por uma pessoa, não consumido pelo fluxo, e satisfazer um
// Requires com ele tornaria o campo decorativo (ADR-0021).
//
// A ordem é o que se verifica, não a existência: um artefato produzido depois da
// etapa que o exige não a satisfaz.
func AuditContract(flow []Stage) []ContractGap {
	available := map[Artifact]bool{TaskID: true}
	var gaps []ContractGap

	for _, stage := range flow {
		if missing := missingFrom(stage.Requires, available); len(missing) > 0 {
			gaps = append(gaps, ContractGap{Stage: stage.ID, Missing: missing})
		}
		for _, produced := range stage.Produces {
			available[produced] = true
		}
	}

	return gaps
}

// missingFrom devolve os artefatos exigidos que ainda não estão disponíveis,
// preservando a ordem da declaração — quem lê o relatório compara com o contrato
// que escreveu.
func missingFrom(required []Artifact, available map[Artifact]bool) []Artifact {
	var missing []Artifact
	for _, r := range required {
		if !available[r] {
			missing = append(missing, r)
		}
	}
	return missing
}
