package routing

import (
	"encoding/json"
	"errors"
	"os"
)

type ModelMap struct {
	entries map[string]string
}

func LoadModelMap(path string) (ModelMap, error) {
	if path == "" {
		return ModelMap{entries: map[string]string{}}, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return ModelMap{entries: map[string]string{}}, nil
		}
		return ModelMap{}, err
	}
	var entries map[string]string
	if err := json.Unmarshal(data, &entries); err != nil {
		return ModelMap{}, err
	}
	if entries == nil {
		entries = map[string]string{}
	}
	return ModelMap{entries: entries}, nil
}

func (m ModelMap) Resolve(clientModel, fallback string, strict bool) (string, bool) {
	if mapped, ok := m.entries[clientModel]; ok && mapped != "" {
		return mapped, true
	}
	if strict {
		return "", false
	}
	return fallback, false
}

func (m ModelMap) Entries() map[string]string {
	out := make(map[string]string, len(m.entries))
	for key, value := range m.entries {
		out[key] = value
	}
	return out
}
