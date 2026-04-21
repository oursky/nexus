package tokenstore

import (
	"fmt"
	"os/exec"
	"strings"
)

const (
	keychainService = "nexus"
	keychainAccount = "daemon-token"
)

// KeychainStore uses the macOS Keychain via the built-in `security` CLI.
type KeychainStore struct{}

func NewKeychainStore() *KeychainStore { return &KeychainStore{} }

func (k *KeychainStore) Load() (string, bool, error) {
	out, err := exec.Command(
		"security", "find-generic-password",
		"-s", keychainService,
		"-a", keychainAccount,
		"-w",
	).Output()
	if err != nil {
		// exit 44 = item not found
		if ee, ok := err.(*exec.ExitError); ok && ee.ExitCode() == 44 {
			return "", false, nil
		}
		return "", false, fmt.Errorf("keychain load: %w", err)
	}
	return strings.TrimSpace(string(out)), true, nil
}

func (k *KeychainStore) Save(token string) error {
	if err := exec.Command(
		"security", "add-generic-password",
		"-s", keychainService,
		"-a", keychainAccount,
		"-w", token,
		"-U", // update if exists
	).Run(); err != nil {
		return fmt.Errorf("keychain save: %w", err)
	}
	return nil
}

func (k *KeychainStore) Delete() error {
	err := exec.Command(
		"security", "delete-generic-password",
		"-s", keychainService,
		"-a", keychainAccount,
	).Run()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok && ee.ExitCode() == 44 {
			return nil // already absent
		}
		return fmt.Errorf("keychain delete: %w", err)
	}
	return nil
}
