package server

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"errors"
	"fmt"
	"os"

	gossh "golang.org/x/crypto/ssh"
)

// EnsureHostKey creates an Ed25519 host key at path when missing, or leaves an existing file untouched.
// The private key is written in OpenSSH PEM format compatible with gliderlabs/ssh HostKeyFile.
func EnsureHostKey(path string) error {
	if path == "" {
		return errors.New("host key path is empty")
	}
	if _, err := os.Stat(path); err == nil {
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("stat host key: %w", err)
	}

	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return fmt.Errorf("generate ed25519 host key: %w", err)
	}

	block, err := gossh.MarshalPrivateKey(priv, "clawssh-host-key")
	if err != nil {
		return fmt.Errorf("marshal host key: %w", err)
	}

	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("open temp host key: %w", err)
	}
	if err := pem.Encode(f, block); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return fmt.Errorf("encode host key: %w", err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("close temp host key: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("rename host key: %w", err)
	}
	return nil
}
