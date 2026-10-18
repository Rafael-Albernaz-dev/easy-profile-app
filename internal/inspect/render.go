package inspect

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

var severityIcon = map[Severity]string{SevOK: "[OK]", SevInfo: "[INFO]", SevWarn: "[ATENÇÃO]", SevBlocker: "[BLOQUEIO]"}

// RenderJSON writes the machine-readable report.
func RenderJSON(w io.Writer, ins *Inspection) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(ins)
}

// RenderText writes the human report in Portuguese.
func RenderText(w io.Writer, ins *Inspection) {
	p := func(format string, a ...any) { fmt.Fprintf(w, format+"\n", a...) }
	g := ins.Graph

	p("EasyProfileApp — inspeção somente leitura (nada foi alterado)")
	p("Fonte do grafo: %s | gerado em %s | parse em %s", ins.Source, ins.GeneratedAt.Format("2006-01-02 15:04:05"), ins.ParseTime)
	p("")
	p("== Caminho do som agora ==")
	p("  Apps → %s → filtros EasyEffects → %s → rota %q", nodeLabel(g.VirtualSink), nodeLabel(g.PhysicalSink), g.ActiveOutputRoute)
	if g.MicResolved != nil {
		p("  %s → filtros do mic → %s → apps de chamada", g.MicResolved.Name, nodeLabel(g.VirtualSource))
	}
	p("")
	p("== Volumes (escala da tela = raiz cúbica do linear) ==")
	for _, item := range []struct {
		label string
		node  *NodeInfo
	}{{"Saída virtual (easyeffects_sink)", g.VirtualSink}, {"Placa física (alsa_output)", g.PhysicalSink},
		{"Fonte virtual (easyeffects_source)", g.VirtualSource}, {"KA-1333 físico", g.MicResolved}} {
		if item.node == nil {
			p("  %-36s ausente", item.label)
			continue
		}
		p("  %-36s %s", item.label, volumeLine(item.node.Volume))
	}
	if a := ins.Alsa; a != nil {
		p("")
		p("== Mixer ALSA (placa %s) ==", a.Card)
		p("  Auto-Mute Mode: %s", a.AutoMute.EnumItem)
		p("  Headphone (P2 frontal, AKG):  %s", mixerLine(a.Headphone))
		p("  Front (P2 traseiro, SP166):   %s", mixerLine(a.Front))
	}
	if ee := ins.EasyEffects; ee != nil {
		p("")
		p("== EasyEffects ==")
		p("  Preset de saída: %s | preset de entrada: %s | bypass: %s", ee.LastOutputPreset, ee.LastInputPreset, boolText(ee.Bypass))
		p("  Destino interno: %s | entrada interna: %s", ee.OutputDevice, ee.InputDevice)
	}
	if inv := ins.Presets; inv != nil {
		p("")
		p("== Presets em disco (somente leitura) ==")
		for _, pf := range inv.Presets {
			comp := strings.Join(pf.Compatible, ",")
			if pf.Direction == "input" {
				comp = "microfone"
			} else if comp == "" {
				comp = "nenhum destino"
			}
			p("  [%s] %-38s → %-14s sha256 %s…", pf.Direction, pf.Name, comp, pf.SHA256[:12])
		}
	}
	p("")
	p("== Diagnóstico ==")
	for _, f := range ins.Findings {
		p("%-11s %s", severityIcon[f.Severity], f.Title)
		if f.Detail != "" {
			p("            %s", f.Detail)
		}
	}
}

func nodeLabel(n *NodeInfo) string {
	if n == nil {
		return "(ausente)"
	}
	return n.Name
}

func volumeLine(v VolumeInfo) string {
	cub, ok := v.CubicPercent()
	if !ok {
		return "sem dados de volume"
	}
	lin, _ := v.LinearAverage()
	return fmt.Sprintf("%3d%% (linear %.3f) mute=%s", cub, lin, boolText(v.Mute))
}

func mixerLine(m MixerControl) string {
	if !m.Found {
		return "não encontrado"
	}
	state := "mudo"
	if m.Audible() {
		state = "AUDÍVEL"
	}
	return fmt.Sprintf("%v%% switch=%v → %s", m.Percent, m.SwitchOn, state)
}

func boolText(b *bool) string {
	if b == nil {
		return "?"
	}
	if *b {
		return "sim"
	}
	return "não"
}
