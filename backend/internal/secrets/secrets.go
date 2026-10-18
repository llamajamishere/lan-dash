// Package secrets handles the master key and AES-256-GCM encryption of
// stored credentials. Plaintext secrets only ever exist in memory.
package secrets

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/user/lan-dash/internal/db"
)

// KeySize is the AES-256 key length in bytes.
const KeySize = 32

// LoadMasterKey reads the master key file. It accepts 64 hex characters
// (as produced by `openssl rand -hex 32`) or exactly 32 raw bytes.
func LoadMasterKey(path string) ([]byte, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("master key %s: %w (run install.sh, or start with -init-key in dev)", path, err)
	}
	if fi.Mode().Perm()&0o077 != 0 {
		return nil, fmt.Errorf("master key %s has insecure permissions %o; run: chmod 600 %s", path, fi.Mode().Perm(), path)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if s := strings.TrimSpace(string(raw)); len(s) == KeySize*2 {
		if key, err := hex.DecodeString(s); err == nil {
			return key, nil
		}
	}
	if len(raw) == KeySize {
		return raw, nil
	}
	return nil, errors.New("master key must be 64 hex chars or 32 raw bytes")
}

// GenerateMasterKey writes a new hex-encoded key with mode 0600 if the file
// does not exist yet. Intended for development (`make dev`).
func GenerateMasterKey(path string) error {
	if _, err := os.Stat(path); err == nil {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	key := make([]byte, KeySize)
	if _, err := rand.Read(key); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(hex.EncodeToString(key)+"\n"), 0o600)
}

// Box encrypts/decrypts with AES-256-GCM. Output layout: nonce || ciphertext.
type Box struct{ aead cipher.AEAD }

// NewBox builds a Box from a 32-byte key.
func NewBox(key []byte) (*Box, error) {
	if len(key) != KeySize {
		return nil, fmt.Errorf("key must be %d bytes", KeySize)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Box{aead: aead}, nil
}

// Encrypt seals plaintext. aad binds the ciphertext to a context (e.g. the
// device id) so blobs cannot be swapped between rows.
func (b *Box) Encrypt(plaintext, aad []byte) ([]byte, error) {
	nonce := make([]byte, b.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return b.aead.Seal(nonce, nonce, plaintext, aad), nil
}

// Decrypt opens a blob produced by Encrypt.
func (b *Box) Decrypt(blob, aad []byte) ([]byte, error) {
	ns := b.aead.NonceSize()
	if len(blob) < ns {
		return nil, errors.New("ciphertext too short")
	}
	return b.aead.Open(nil, blob[:ns], blob[ns:], aad)
}

// CredentialStore persists device credentials encrypted at rest.
type CredentialStore struct {
	db  *db.DB
	box *Box
}

// NewCredentialStore creates a store.
func NewCredentialStore(d *db.DB, box *Box) *CredentialStore {
	return &CredentialStore{db: d, box: box}
}

type secretPayload struct {
	Password   string `json:"password,omitempty"`
	PrivateKey string `json:"private_key,omitempty"`
	Passphrase string `json:"passphrase,omitempty"`
}

func aad(deviceID int64) []byte { return []byte(fmt.Sprintf("landash-cred:%d", deviceID)) }

// Get returns the decrypted credential for a device.
func (s *CredentialStore) Get(ctx context.Context, deviceID int64) (*db.Credential, error) {
	kind, user, enc, _, err := s.db.GetCredentialRow(ctx, deviceID)
	if err != nil {
		return nil, err
	}
	c := &db.Credential{DeviceID: deviceID, Kind: kind, Username: user}
	if len(enc) > 0 {
		plain, err := s.box.Decrypt(enc, aad(deviceID))
		if err != nil {
			return nil, fmt.Errorf("decrypt credential: %w", err)
		}
		var p secretPayload
		if err := json.Unmarshal(plain, &p); err != nil {
			return nil, err
		}
		c.Password, c.PrivateKey, c.Passphrase = p.Password, p.PrivateKey, p.Passphrase
	}
	return c, nil
}

// Info returns the browser-safe view (no secret material).
func (s *CredentialStore) Info(ctx context.Context, deviceID int64) (*db.CredentialInfo, error) {
	c, err := s.Get(ctx, deviceID)
	if err != nil {
		return nil, err
	}
	_, _, _, updated, _ := s.db.GetCredentialRow(ctx, deviceID)
	return &db.CredentialInfo{DeviceID: deviceID, Kind: c.Kind, Username: c.Username,
		HasPassword: c.Password != "", HasPrivateKey: c.PrivateKey != "", UpdatedAt: updated}, nil
}

// Put encrypts and stores a credential. Empty secret fields keep the
// existing stored value unless clear is true, so the UI can update the
// username without re-entering the key.
func (s *CredentialStore) Put(ctx context.Context, c *db.Credential, clear bool) error {
	if !clear {
		if old, err := s.Get(ctx, c.DeviceID); err == nil {
			if c.Password == "" {
				c.Password = old.Password
			}
			if c.PrivateKey == "" {
				c.PrivateKey, c.Passphrase = old.PrivateKey, old.Passphrase
			}
		}
	}
	plain, err := json.Marshal(secretPayload{Password: c.Password, PrivateKey: c.PrivateKey, Passphrase: c.Passphrase})
	if err != nil {
		return err
	}
	enc, err := s.box.Encrypt(plain, aad(c.DeviceID))
	if err != nil {
		return err
	}
	if c.Kind == "" {
		c.Kind = "ssh"
	}
	return s.db.PutCredentialRow(ctx, c.DeviceID, c.Kind, c.Username, enc)
}

// Delete removes a credential.
func (s *CredentialStore) Delete(ctx context.Context, deviceID int64) error {
	return s.db.DeleteCredential(ctx, deviceID)
}
