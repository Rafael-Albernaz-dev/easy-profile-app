package pipewire

import (
	"encoding/json"
	"io"
	"strings"
)

// Deltas only wake the worker; they never become a cached graph observation.
func (r *RealPipeWire) watchGraphEvents(reader io.Reader, events chan<- struct{}) error {
	decoder := json.NewDecoder(reader)
	types := map[int]string{}
	defaultMetadata := map[int]bool{}
	for {
		var event []struct {
			ID    int             `json:"id"`
			Type  string          `json:"type"`
			Info  json.RawMessage `json:"info"`
			Props struct {
				Name string `json:"metadata.name"`
			} `json:"props"`
			Metadata []struct {
				Subject int    `json:"subject"`
				Key     string `json:"key"`
			} `json:"metadata"`
		}
		if err := decoder.Decode(&event); err != nil {
			return err
		}
		relevant := false
		for _, obj := range event {
			kind := obj.Type
			if kind == "" {
				kind = types[obj.ID]
			} else {
				types[obj.ID] = kind
			}
			removed := string(obj.Info) == "null"
			if obj.Props.Name == "default" {
				defaultMetadata[obj.ID] = true
				relevant = true
			}
			// Default changes can arrive without Node/Link/Device changes.
			// Include configured values, effective values and deletions (null).
			for _, entry := range obj.Metadata {
				if entry.Subject != 0 {
					continue
				}
				switch entry.Key {
				case "default.audio.source", "default.configured.audio.source", "default.audio.sink", "default.configured.audio.sink":
					defaultMetadata[obj.ID] = true
					relevant = true
				}
			}
			isAudioObject := strings.HasSuffix(kind, ":Node") || strings.HasSuffix(kind, ":Link") || strings.HasSuffix(kind, ":Device")
			if isAudioObject || (removed && (kind == "" || defaultMetadata[obj.ID])) {
				relevant = true
			}
			if removed {
				if strings.HasSuffix(kind, ":Node") || strings.HasSuffix(kind, ":Device") || kind == "" {
					r.invalidateObject(obj.ID)
				}
				delete(types, obj.ID)
				delete(defaultMetadata, obj.ID)
			}
		}
		if relevant {
			select {
			case events <- struct{}{}:
			default:
			}
		}
	}
}
