package fsm

import "testing"

// flowIntegro devolve um fluxo curto em que todo Requires tem produtor anterior.
// Fake nomeado: vários cenários partem dele e alteram um ponto de cada vez.
func flowIntegro() []Stage {
	return []Stage{
		{ID: "discovery", Requires: []Artifact{TaskID}, Produces: []Artifact{"repos"}},
		{ID: "setup", Requires: []Artifact{"repos"}, Produces: []Artifact{"worktree"}},
		{ID: "intake", Requires: []Artifact{TaskID, "worktree"}, Produces: []Artifact{"briefing"}},
	}
}

// TestFluxoIntegroNaoAcusaLacuna cobre o cenário B1.
//
// Percorrendo as etapas em ordem, todo artefato exigido foi produzido por alguma
// etapa anterior. Um fluxo assim está íntegro no papel, e a auditoria não
// reporta nada.
func TestFluxoIntegroNaoAcusaLacuna(t *testing.T) {
	gaps := AuditContract(flowIntegro())

	if len(gaps) != 0 {
		t.Errorf("fluxo íntegro não deveria acusar lacuna, veio %v", gaps)
	}
}

// TestExigenciaSemProdutorEAcusada cobre o cenário B2.
//
// Uma etapa que exige um artefato que ninguém antes produz quebra o fluxo no
// papel. A auditoria acusa nomeando a etapa e o artefato faltante — sem os dois,
// a mensagem custa uma sessão de depuração.
func TestExigenciaSemProdutorEAcusada(t *testing.T) {
	flow := []Stage{
		{ID: "discovery", Requires: []Artifact{TaskID}, Produces: []Artifact{"repos"}},
		{ID: "build", Requires: []Artifact{"approach"}, Produces: []Artifact{"code"}},
	}

	gaps := AuditContract(flow)

	if len(gaps) != 1 {
		t.Fatalf("quis 1 lacuna, veio %d (%v)", len(gaps), gaps)
	}
	if gaps[0].Stage != "build" {
		t.Errorf("Stage da lacuna: quis build, veio %q", gaps[0].Stage)
	}
	if len(gaps[0].Missing) != 1 || gaps[0].Missing[0] != "approach" {
		t.Errorf("Missing: quis [approach], veio %v", gaps[0].Missing)
	}
}

// TestArtefatoDeAuditoriaNaoSatisfazExigencia cobre o cenário B3.
//
// Um ProducesForHuman não entra no conjunto de artefatos disponíveis: ele é lido
// por uma pessoa, não consumido pelo fluxo. Uma etapa que exija um artefato de
// auditoria está exigindo algo que o fluxo não entrega, e isso é lacuna.
//
// É o teste que prova que ProducesForHuman faz alguma coisa: se ele passasse a
// satisfazer Requires, o campo seria decorativo.
func TestArtefatoDeAuditoriaNaoSatisfazExigencia(t *testing.T) {
	flow := []Stage{
		{ID: "verify", Requires: []Artifact{TaskID}, Produces: []Artifact{"ci_green"}, ProducesForHuman: []Artifact{"dod_checked"}},
		{ID: "commit", Requires: []Artifact{"dod_checked"}, Produces: []Artifact{"commit_sha"}},
	}

	gaps := AuditContract(flow)

	if len(gaps) != 1 {
		t.Fatalf("artefato de auditoria não satisfaz Requires; quis 1 lacuna, veio %d (%v)", len(gaps), gaps)
	}
	if gaps[0].Stage != "commit" || gaps[0].Missing[0] != "dod_checked" {
		t.Errorf("quis lacuna commit/dod_checked, veio %s/%v", gaps[0].Stage, gaps[0].Missing)
	}
}

// TestArtefatoProduzidoDepoisNaoSatisfaz cobre o cenário B4.
//
// A ordem importa: um artefato produzido por uma etapa posterior não está
// disponível para a anterior. A auditoria acusa mesmo existindo produtor no
// conjunto — o que ela verifica é a precedência, não a existência.
func TestArtefatoProduzidoDepoisNaoSatisfaz(t *testing.T) {
	flow := []Stage{
		{ID: "build", Requires: []Artifact{"scenarios"}, Produces: []Artifact{"code"}},
		{ID: "scenarios", Requires: []Artifact{TaskID}, Produces: []Artifact{"scenarios"}},
	}

	gaps := AuditContract(flow)

	if len(gaps) != 1 {
		t.Fatalf("quis 1 lacuna por ordem invertida, veio %d (%v)", len(gaps), gaps)
	}
	if gaps[0].Stage != "build" {
		t.Errorf("quis lacuna em build, veio %q", gaps[0].Stage)
	}
}

// TestTaskIDEstaDisponivelDesdeOInicio cobre o cenário B5.
//
// A tarefa já chega com seu identificador: é a raiz do grafo e o único insumo
// que nenhuma etapa produz. Qualquer outro artefato precisa de produtor
// declarado.
func TestTaskIDEstaDisponivelDesdeOInicio(t *testing.T) {
	flow := []Stage{
		{ID: "discovery", Requires: []Artifact{TaskID}, Produces: []Artifact{"repos"}},
	}

	if gaps := AuditContract(flow); len(gaps) != 0 {
		t.Errorf("task_id é a raiz e não deveria acusar lacuna, veio %v", gaps)
	}

	semRaiz := []Stage{
		{ID: "discovery", Requires: []Artifact{"outra_coisa"}, Produces: []Artifact{"repos"}},
	}

	if gaps := AuditContract(semRaiz); len(gaps) != 1 {
		t.Errorf("só task_id é insumo externo; quis 1 lacuna, veio %v", gaps)
	}
}

// TestTodasAsLacunasSaoReportadas cobre o cenário B6.
//
// A auditoria percorre o fluxo inteiro e reporta tudo que encontra. Parar na
// primeira lacuna faria quem conserta descobrir as demais uma a uma, a cada
// nova execução.
func TestTodasAsLacunasSaoReportadas(t *testing.T) {
	flow := []Stage{
		{ID: "a", Requires: []Artifact{"faltante_um"}, Produces: []Artifact{"x"}},
		{ID: "b", Requires: []Artifact{"faltante_dois"}, Produces: []Artifact{"y"}},
		{ID: "c", Requires: []Artifact{"faltante_tres"}, Produces: []Artifact{"z"}},
	}

	gaps := AuditContract(flow)

	if len(gaps) != 3 {
		t.Fatalf("quis 3 lacunas reportadas juntas, veio %d (%v)", len(gaps), gaps)
	}
	for i, quer := range []StageID{"a", "b", "c"} {
		if gaps[i].Stage != quer {
			t.Errorf("lacuna %d: quis etapa %q, veio %q", i, quer, gaps[i].Stage)
		}
	}
}

// TestLacunaComVariosArtefatosAgrupaPorEtapa cobre a borda de B6.
//
// Uma etapa a que faltam dois insumos gera uma lacuna com os dois, não duas
// lacunas — quem lê o relatório quer saber o que falta para a etapa começar.
func TestLacunaComVariosArtefatosAgrupaPorEtapa(t *testing.T) {
	flow := []Stage{
		{ID: "build", Requires: []Artifact{"scenarios", "approach"}, Produces: []Artifact{"code"}},
	}

	gaps := AuditContract(flow)

	if len(gaps) != 1 {
		t.Fatalf("quis 1 lacuna agrupada por etapa, veio %d (%v)", len(gaps), gaps)
	}
	if len(gaps[0].Missing) != 2 {
		t.Errorf("Missing: quis 2 artefatos na mesma lacuna, veio %v", gaps[0].Missing)
	}
}

// TestFluxoVazioNaoAcusaLacuna cobre a borda degenerada.
//
// Um fluxo sem etapas não tem exigência a violar. É caso limite, mas quem chama
// não deveria precisar tratá-lo por fora.
func TestFluxoVazioNaoAcusaLacuna(t *testing.T) {
	if gaps := AuditContract(nil); len(gaps) != 0 {
		t.Errorf("fluxo vazio não tem o que acusar, veio %v", gaps)
	}
}
