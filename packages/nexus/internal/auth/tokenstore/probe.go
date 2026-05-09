//go:build !darwin

package tokenstore

import "runtime"

func probe() Store {
	if runtime.GOOS == "linux" {
		if secretServiceAvailable() {
			if ss, err := NewSecretServiceStore(); err == nil {
				return ss
			}
		}
	}
	return NewFileStore(DefaultLinuxFilePath())
}
