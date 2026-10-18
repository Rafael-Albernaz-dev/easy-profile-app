package inspect

import (
	"fmt"
	"sort"
	"strings"
)

type Severity string

const (
	SevOK      Severity = "OK"
	SevInfo    Severity = "INFO"
	SevWarn    Severity = "ATENCAO"
	SevBlocker Severity = "BLOQUEIO"
)

// Finding is one diagnostic. Detail explains in plain language what it means
// for the sound, because the report is read by a developer, not an audio engineer.
type Finding struct {
	Code     string   `json:"code"`
	Severity Severity `json:"severity"`
	Title    string   `json:"title"`
	Detail   string   `json:"detail"`
}

// Evaluate turns raw probe data into findings. Pure function: no I/O.
func Evaluate(ins *Inspection) []Finding {
	var f []Finding
	add := func(code string, sev Severity, title, detail string) {
		f = append(f, Finding{Code: code, Severity: sev, Title: title, Detail: detail})
	}
	g := ins.Graph
	id := ins.Identity

	// --- placa e rota ---
	if !g.CardFound {
		add("CARD", SevBlocker, "Placa integrada não encontrada",
			"Não há dispositivo "+id.CardName+" no grafo. Sem ela não existe nem AKG nem caixas.")
	} else {
		label := map[string]string{DestAKG: "AKG K52 (P2 frontal)", DestSP166: "Caixas SP166 (P2 traseiro)"}[g.ActiveDestination]
		if label == "" {
			add("ROUTE_ACTIVE", SevWarn, "Rota de saída ativa desconhecida: "+g.ActiveOutputRoute,
				"A placa está numa rota que não é nem fone nem caixas.")
		} else {
			add("ROUTE_ACTIVE", SevOK, "Destino ativo: "+label,
				"A placa integrada tem UMA saída física com duas 'portas' (rotas). Hoje a rota selecionada é "+
					g.ActiveOutputRoute+". Trocar de destino = trocar a rota, não trocar de dispositivo.")
		}
		var parts []string
		allFound := true
		for _, o := range g.RouteOptions {
			if !o.Found {
				allFound = false
				parts = append(parts, o.Name+": ausente")
				continue
			}
			parts = append(parts, fmt.Sprintf("%s → índice %d (disponível: %s)", o.Name, o.Index, o.Available))
		}
		sev := SevOK
		if !allFound {
			sev = SevBlocker
		}
		add("ROUTE_INDEXES", sev, "Rotas oferecidas pela placa (EnumRoute)",
			strings.Join(parts, "; ")+". Esses índices são lidos a cada operação; nunca viram constante no código.")
	}

	// --- microfone ---
	switch len(g.MicCandidates) {
	case 1:
		add("MIC_RESOLVE", SevOK, "KA-1333 identificado pelo serial USB",
			"Fonte atual: "+g.MicResolved.Name+". O sufixo numérico do nome pode mudar entre reconexões; "+
				"por isso a busca é feita pelo serial USB, igual ao guard instalado.")
	case 0:
		add("MIC_RESOLVE", SevBlocker, "KA-1333 ausente",
			"Nenhuma fonte com o serial "+id.MicSerial+" e perfil "+id.MicProfile+". O app não escolheria outro microfone no lugar.")
	default:
		names := make([]string, 0, len(g.MicCandidates))
		for _, n := range g.MicCandidates {
			names = append(names, n.Name)
		}
		add("MIC_RESOLVE", SevBlocker, "KA-1333 ambíguo",
			"Mais de uma fonte bate com o serial: "+strings.Join(names, ", ")+". O app se recusaria a escolher.")
	}

	// --- nós virtuais do EasyEffects: o achado crítico da auditoria ---
	for _, item := range []struct {
		code string
		node *NodeInfo
		what string
	}{
		{"VSINK_MONITOR_VOLUMES", g.VirtualSink, "saída virtual (easyeffects_sink)"},
		{"VSOURCE_MONITOR_VOLUMES", g.VirtualSource, "fonte virtual (easyeffects_source)"},
	} {
		if item.node == nil {
			add(item.code, SevBlocker, "Nó "+item.what+" não encontrado", "O EasyEffects pode não estar rodando.")
			continue
		}
		if item.node.MonitorChannelVolumes != nil && !*item.node.MonitorChannelVolumes {
			add(item.code, SevWarn, "monitor.channel-volumes = false na "+item.what,
				"O som que sai deste nó para o resto da cadeia passa pelas portas de 'monitor'. Com essa opção "+
					"desligada, mudar o volume/mute normal do nó pode mudar o número exibido SEM atenuar o som "+
					"nesse caminho. Contrato a provar com escuta acompanhada na EP-002 antes de usar este controle.")
		} else {
			add(item.code, SevOK, "monitor.channel-volumes não está desligado na "+item.what, "")
		}
	}
	if vs := g.VirtualSink; vs != nil {
		req, okReq := vs.Volume.CubicPercent()
		soft, okSoft := average(vs.Volume.SoftVolumes)
		if okReq && okSoft && vs.Volume.SoftDiffersFromChannel() {
			add("VSINK_SOFT_DIVERGENCE", SevInfo, "Volume pedido ≠ ganho aplicado no easyeffects_sink",
				fmt.Sprintf("O volume pedido aparece como %d%% (wpctl), mas o ganho de software aplicado pelo próprio nó "+
					"é %.2f (linear). Indício coerente com o item acima: a atenuação pode estar acontecendo em outro "+
					"ponto (ex.: no aplicativo/stream) ou não acontecer. Só a escuta da EP-002 decide.", req, soft))
		}
	}

	// --- volumes físicos (escala) ---
	if ps := g.PhysicalSink; ps != nil {
		lin, _ := ps.Volume.LinearAverage()
		cub, _ := ps.Volume.CubicPercent()
		add("PHYSICAL_VOLUME", SevInfo, fmt.Sprintf("Volume físico da placa: %d%% (linear %.3f)", cub, lin),
			"PipeWire guarda ganho LINEAR; wpctl/GNOME mostram a raiz cúbica. Ex.: linear 0,848 = 95% na tela. "+
				"Comparações sempre na mesma escala.")
	}

	// --- defaults e desvios ---
	if g.DefaultSink == id.VirtualSink {
		add("DEFAULT_SINK", SevOK, "Saída padrão dos apps = easyeffects_sink",
			"Apps que tocam no padrão passam pelo tratamento antes de chegar na placa.")
	} else {
		add("DEFAULT_SINK", SevWarn, "Saída padrão = "+g.DefaultSink,
			"Apps que seguem o padrão podem tocar sem o tratamento do EasyEffects.")
	}
	if g.MicResolved != nil && strings.HasPrefix(g.DefaultSource, strings.TrimSuffix(g.MicResolved.Name, nodeSuffix(g.MicResolved.Name))) {
		add("DEFAULT_SOURCE_RAW", SevInfo, "Microfone padrão do sistema = KA-1333 físico (cru)",
			"A fonte padrão é o mic sem tratamento. Com process-all-inputs ligado, o EasyEffects tende a "+
				"redirecionar as capturas, mas isso não é garantia por app. O alerta de 'mic cru' (links reais) é quem prova.")
	}
	if len(g.RawMicCaptures) > 0 {
		add("RAW_CAPTURES", SevWarn, fmt.Sprintf("%d app(s) lendo o microfone cru", len(g.RawMicCaptures)),
			"Capturando direto do KA-1333 sem filtros: "+describeCaptures(g.RawMicCaptures)+".")
	} else {
		add("RAW_CAPTURES", SevOK, "Nenhum app lendo o microfone cru agora",
			"Só reflete este instante: sem chamada aberta, não há captura para avaliar.")
	}
	if len(g.PlaybackIntoPhysical) > 0 {
		add("PLAYBACK_BYPASS", SevWarn, "App tocando direto na placa (sem EasyEffects)", describeCaptures(g.PlaybackIntoPhysical))
	}
	if len(g.RestoreToPhysical) > 0 {
		keys := make([]string, 0, len(g.RestoreToPhysical))
		for k := range g.RestoreToPhysical {
			keys = append(keys, strings.TrimPrefix(k, "restore.stream."))
		}
		sort.Strings(keys)
		add("RESTORE_TO_PHYSICAL", SevInfo, "WirePlumber lembra stream(s) apontando para a placa física",
			"Classes: "+strings.Join(keys, ", ")+". Ex.: sons de notificação podem ir direto para a placa, "+
				"sem passar pelo tratamento e sem obedecer o volume virtual. Apenas observado; nada alterado.")
	}

	// --- ALSA ---
	if a := ins.Alsa; a != nil {
		if a.AutoMute.EnumItem == "Disabled" {
			add("ALSA_AUTOMUTE", SevOK, "Auto-Mute Mode = Disabled",
				"Pré-condição da troca: permite usar a traseira mesmo com o fone plugado na frente.")
		} else {
			add("ALSA_AUTOMUTE", SevWarn, "Auto-Mute Mode = "+a.AutoMute.EnumItem,
				"Com auto-mute ligado, plugar o fone pode cortar as caixas por hardware, fora do controle do app.")
		}
		active, inactive := a.Front, a.Headphone // SP166 usa "Front" (traseira)
		if g.ActiveDestination == DestAKG {
			active, inactive = a.Headphone, a.Front
		}
		switch {
		case g.ActiveDestination == DestUnknown:
		case active.Audible() && !inactive.Audible():
			add("ALSA_PINS", SevOK, fmt.Sprintf("Pinos coerentes: %s ligado, %s mudo", active.Name, inactive.Name),
				"Só o destino ativo pode soar; o outro P2 está silenciado no hardware.")
		case inactive.Audible():
			add("ALSA_PINS", SevWarn, "Pino do destino inativo está audível ("+inactive.Name+")",
				"O som pode vazar pelo P2 que não deveria tocar.")
		default:
			add("ALSA_PINS", SevWarn, "Pino do destino ativo está mudo ("+active.Name+")",
				"A rota está certa, mas o hardware desse P2 está silenciado.")
		}
	}

	// --- EasyEffects ---
	if ee := ins.EasyEffects; ee != nil {
		if ee.Bypass != nil && *ee.Bypass {
			add("EE_BYPASS", SevWarn, "EasyEffects em bypass global",
				"Todos os filtros (inclusive os do microfone) estão desligados.")
		} else if ee.Bypass != nil {
			add("EE_BYPASS", SevOK, "Bypass global desligado", "Os filtros estão ativos.")
		}
		if ee.OutputDevice == id.PhysicalSink && ee.UseDefaultOutputDevice != nil && !*ee.UseDefaultOutputDevice {
			add("EE_OUTPUT_DEVICE", SevOK, "Destino interno do EasyEffects = placa integrada",
				"O som tratado sai para a placa física (o que o guard de saída mantém).")
		} else {
			add("EE_OUTPUT_DEVICE", SevWarn, "Destino interno do EasyEffects = "+ee.OutputDevice, "")
		}
		if g.MicResolved != nil {
			if ee.InputDevice == g.MicResolved.Name && ee.UseDefaultInputDevice != nil && !*ee.UseDefaultInputDevice {
				add("EE_INPUT_DEVICE", SevOK, "Entrada interna do EasyEffects = KA-1333",
					"Os filtros do microfone recebem o KA-1333 (o que o guard de entrada mantém).")
			} else {
				add("EE_INPUT_DEVICE", SevWarn, "Entrada interna do EasyEffects = "+ee.InputDevice,
					"Diferente do KA-1333 resolvido ("+g.MicResolved.Name+"). Foi esse o sintoma da perda de retorno no setup.")
			}
		}
		if g.ActiveDestination != DestUnknown && ee.LastOutputPreset != "" {
			compatible := false
			for _, d := range CompatibleDestinations(ee.LastOutputPreset) {
				if d == g.ActiveDestination {
					compatible = true
				}
			}
			if compatible {
				add("EE_PRESET_MATCH", SevOK, "Preset de saída coerente com o destino: "+ee.LastOutputPreset,
					"Leitura do último preset carregado. Nome coerente não prova cadeia pronta; a EP-002 confere plugins/links.")
			} else {
				add("EE_PRESET_MATCH", SevWarn, "Preset de saída NÃO combina com o destino: "+ee.LastOutputPreset,
					"Correção de um equipamento pode estar sendo aplicada no outro.")
			}
		}
	}

	if inv := ins.Presets; inv != nil {
		if len(inv.Collisions) > 0 {
			add("PRESET_COLLISION", SevWarn, "Nome de preset repetido em entrada e saída",
				strings.Join(inv.Collisions, ", ")+": `easyeffects -l` procura primeiro na entrada e carregaria a cadeia errada.")
		}
		var orphan []string
		for _, p := range inv.Presets {
			if p.Direction == "output" && len(p.Compatible) == 0 {
				orphan = append(orphan, p.Name)
			}
		}
		if len(orphan) > 0 {
			add("PRESET_UNCLASSIFIED", SevInfo, "Preset(s) de saída sem prefixo AKG/SP166/Caixas",
				strings.Join(orphan, ", ")+": não apareceriam em nenhum dropdown (padrão seguro).")
		}
	}

	if ins.Guards != nil {
		var bad []string
		for _, u := range GuardUnits {
			if ins.Guards[u] != "active" {
				bad = append(bad, u+"="+ins.Guards[u])
			}
		}
		if len(bad) == 0 {
			add("GUARDS", SevOK, "Os 2 guards de produção estão ativos", "Nada do setup atual foi substituído.")
		} else {
			add("GUARDS", SevWarn, "Guard(s) fora do estado esperado", strings.Join(bad, ", "))
		}
	}
	for _, e := range ins.ProbeErrors {
		add("PROBE_ERROR", SevWarn, "Falha de leitura", e)
	}
	return f
}

// nodeSuffix returns a trailing ".N" instance suffix (e.g. ".3"), or "".
func nodeSuffix(name string) string {
	i := strings.LastIndex(name, ".")
	if i < 0 || i == len(name)-1 {
		return ""
	}
	for _, r := range name[i+1:] {
		if r < '0' || r > '9' {
			return ""
		}
	}
	return name[i:]
}

func describeCaptures(cs []Capture) string {
	parts := make([]string, 0, len(cs))
	for _, c := range cs {
		label := c.NodeName
		if c.App != "" && c.App != c.NodeName {
			label = c.App + " (" + c.NodeName + ")"
		}
		parts = append(parts, label)
	}
	return strings.Join(parts, ", ")
}
