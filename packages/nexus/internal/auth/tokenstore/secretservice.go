//go:build !nodbus

package tokenstore

import (
	"fmt"
	"os"

	"github.com/godbus/dbus/v5"
)

const (
	ssService         = "org.freedesktop.secrets"
	ssServicePath     = dbus.ObjectPath("/org/freedesktop/secrets")
	ssCollectionPath  = dbus.ObjectPath("/org/freedesktop/secrets/aliases/default")
	ssServiceIface    = "org.freedesktop.Secret.Service"
	ssCollectionIface = "org.freedesktop.Secret.Collection"
	ssItemIface       = "org.freedesktop.Secret.Item"
	ssSessionIface    = "org.freedesktop.Secret.Session"

	ssServiceName = "nexus"
	ssAccount     = "daemon-token"
	ssLabel       = "nexus/daemon-token"
)

type ssSecret struct {
	Session     dbus.ObjectPath
	Parameters  []byte
	Value       []byte
	ContentType string
}

type SecretServiceStore struct {
	conn    *dbus.Conn
	session dbus.ObjectPath
}

func NewSecretServiceStore() (*SecretServiceStore, error) {
	if os.Getenv("DBUS_SESSION_BUS_ADDRESS") == "" {
		return nil, fmt.Errorf("D-Bus session bus not available: DBUS_SESSION_BUS_ADDRESS not set")
	}
	conn, err := dbus.SessionBus()
	if err != nil {
		return nil, fmt.Errorf("D-Bus session bus unavailable: %w", err)
	}

	svc := conn.Object(ssService, ssServicePath)
	var sessionPath dbus.ObjectPath
	var output dbus.Variant
	if err := svc.Call(ssServiceIface+".OpenSession", 0, "plain", dbus.MakeVariant("")).Store(&output, &sessionPath); err != nil {
		conn.Close()
		return nil, fmt.Errorf("secret service OpenSession: %w", err)
	}

	return &SecretServiceStore{conn: conn, session: sessionPath}, nil
}

func IsAvailable() bool {
	if os.Getenv("DBUS_SESSION_BUS_ADDRESS") == "" {
		return false
	}
	conn, err := dbus.SessionBus()
	if err != nil {
		return false
	}
	conn.Close()
	return true
}

func (s *SecretServiceStore) Load() (string, bool, error) {
	items, err := s.searchItems()
	if err != nil {
		return "", false, err
	}
	if len(items) == 0 {
		return "", false, nil
	}

	if err := s.unlockObjects(items); err != nil {
		return "", false, err
	}

	item := s.conn.Object(ssService, items[0])
	var secret ssSecret
	if err := item.Call(ssItemIface+".GetSecret", 0, s.session).Store(&secret); err != nil {
		return "", false, fmt.Errorf("secret service GetSecret: %w", err)
	}
	return string(secret.Value), true, nil
}

func (s *SecretServiceStore) Save(token string) error {
	if err := s.unlockObjects([]dbus.ObjectPath{ssCollectionPath}); err != nil {
		return err
	}

	properties := map[string]dbus.Variant{
		"org.freedesktop.Secret.Item.Label": dbus.MakeVariant(ssLabel),
		"org.freedesktop.Secret.Item.Attributes": dbus.MakeVariant(map[string]string{
			"service": ssServiceName,
			"account": ssAccount,
		}),
	}
	secret := ssSecret{
		Session:     s.session,
		Parameters:  []byte{},
		Value:       []byte(token),
		ContentType: "text/plain",
	}

	coll := s.conn.Object(ssService, ssCollectionPath)
	var itemPath dbus.ObjectPath
	var prompt dbus.ObjectPath
	if err := coll.Call(ssCollectionIface+".CreateItem", 0, properties, secret, true).Store(&itemPath, &prompt); err != nil {
		return fmt.Errorf("secret service CreateItem: %w", err)
	}
	if prompt != dbus.ObjectPath("/") {
		if err := s.handlePrompt(prompt); err != nil {
			return err
		}
	}
	return nil
}

func (s *SecretServiceStore) searchItems() ([]dbus.ObjectPath, error) {
	attrs := map[string]string{
		"service": ssServiceName,
		"account": ssAccount,
	}
	svc := s.conn.Object(ssService, ssServicePath)
	var unlocked, locked []dbus.ObjectPath
	if err := svc.Call(ssServiceIface+".SearchItems", 0, attrs).Store(&unlocked, &locked); err != nil {
		return nil, fmt.Errorf("secret service SearchItems: %w", err)
	}
	return append(unlocked, locked...), nil
}

func (s *SecretServiceStore) unlockObjects(paths []dbus.ObjectPath) error {
	svc := s.conn.Object(ssService, ssServicePath)
	var unlocked []dbus.ObjectPath
	var prompt dbus.ObjectPath
	if err := svc.Call(ssServiceIface+".Unlock", 0, paths).Store(&unlocked, &prompt); err != nil {
		return fmt.Errorf("secret service Unlock: %w", err)
	}
	if prompt != dbus.ObjectPath("/") {
		return s.handlePrompt(prompt)
	}
	return nil
}

func (s *SecretServiceStore) handlePrompt(promptPath dbus.ObjectPath) error {
	promptObj := s.conn.Object(ssService, promptPath)
	if err := promptObj.Call("org.freedesktop.Secret.Prompt.Prompt", 0, "").Err; err != nil {
		return fmt.Errorf("secret service Prompt: %w", err)
	}
	return nil
}
