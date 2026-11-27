package pipewire

import (
	"context"
	"fmt"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/toru/easy-profile-app/internal/inspect"
)

// PrepareHDMIOutput checks the configured port before the daemon changes its
// destination. A port may be available while its card profile is inactive.
func (r *RealPipeWire) PrepareHDMIOutput(ctx context.Context, cardName, sinkName, routeName string) error {
	ctx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()
	g, err := r.graph(ctx)
	if err != nil {
		return err
	}
	deviceID, profile, err := hdmiOutput(g, cardName, sinkName, routeName)
	if err != nil || profile == nil {
		return err
	}

	props := fmt.Sprintf(`{"index": %d, "save": false}`, profile.Index)
	if out, err := exec.CommandContext(ctx, "pw-cli", "s", strconv.Itoa(deviceID), "Profile", props).CombinedOutput(); err != nil {
		return fmt.Errorf("ativar perfil HDMI: %w: %s", err, out)
	}
	r.InvalidateCache()

	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("saída HDMI %q não apareceu após ativar o perfil: %w", sinkName, ctx.Err())
		case <-ticker.C:
			g, err = r.graph(ctx)
			if err != nil {
				return err
			}
			_, pending, err := hdmiOutput(g, cardName, sinkName, routeName)
			if err != nil || pending == nil {
				return err
			}
		}
	}
}

// hdmiOutput resolves only the configured card, route and sink profile. The
// other HDMI/DisplayPort routes on the same card can belong to other monitors.
func hdmiOutput(g *inspect.Graph, cardName, sinkName, routeName string) (int, *inspect.ProfileInfo, error) {
	for _, device := range g.Devices {
		if device.Name != cardName {
			continue
		}
		for _, route := range device.EnumRoutes {
			if route.Direction != "Output" || route.Name != routeName {
				continue
			}
			if route.Available != "yes" {
				break
			}
			for _, node := range g.Nodes {
				if node.DeviceID == device.ID && node.Name == sinkName && node.MediaClass == "Audio/Sink" {
					return device.ID, nil, nil
				}
			}
			card, ok := strings.CutPrefix(cardName, "alsa_card.")
			profileName, matches := strings.CutPrefix(sinkName, "alsa_output."+card+".")
			if ok && matches {
				for _, profile := range device.EnumProfiles {
					if profile.Name == "output:"+profileName && profile.Available == "yes" && slices.Contains(route.Profiles, profile.Index) {
						return device.ID, &profile, nil
					}
				}
			}
			return 0, nil, fmt.Errorf("rota %q disponível, mas perfil da saída %q ausente", routeName, sinkName)
		}
		return 0, nil, fmt.Errorf("rota %q indisponível. Confira a conexão HDMI e a imagem na TV", routeName)
	}
	return 0, nil, fmt.Errorf("placa de áudio HDMI %q não detectada", cardName)
}
