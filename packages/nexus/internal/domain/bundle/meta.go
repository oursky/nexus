package bundle

import (
	"encoding/json"
	"fmt"
)

// BundleMeta is the minimal metadata stored inside an NXPACK bundle's assets tar
// as meta.json. It replaces the old manifest.json with a rigid directory structure.
type BundleMeta struct {
	Arch    []string `json:"arch"`
	Bake    []string `json:"bake"`
	Init    []string `json:"init"`
	Up      []string `json:"up"`
	Down    []string `json:"down"`
	Ports   []int    `json:"ports"`
	CPUs    uint8    `json:"cpus"`
	Memory  uint32   `json:"memory"`
	Workdir string   `json:"workdir"`
}

// DefaultWorkdir returns the working directory for the bundle VM.
func (m BundleMeta) DefaultWorkdir() string {
	if m.Workdir != "" {
		return m.Workdir
	}
	return "/workspace"
}

// ParseMeta deserialises meta.json bytes.
func ParseMeta(data []byte) (BundleMeta, error) {
	var m BundleMeta
	if err := json.Unmarshal(data, &m); err != nil {
		return BundleMeta{}, fmt.Errorf("bundle: parse meta: %w", err)
	}
	return m, nil
}

// MarshalMeta serialises m to indented JSON.
func MarshalMeta(m BundleMeta) ([]byte, error) {
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("bundle: marshal meta: %w", err)
	}
	return data, nil
}
