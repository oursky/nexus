//go:build e2e && !darwin

package harness

func adhocSignNexusForHypervisor(binPath string) error {
	_ = binPath
	return nil
}
