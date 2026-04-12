package storage

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"golang.org/x/crypto/argon2"
)

// EncryptionFormatVersion is the current encryption wire format version.
const EncryptionFormatVersion = 1

// TODO: Key rotation (future phase)
// - Accept old key + new key
// - Decrypt all messages with old key, re-encrypt with new key
// - Re-encrypt audit log entries
// - Atomic file replacement for key/salt files
// - Rollback on partial failure

// Encryptor provides AES-256-GCM encryption using a single master key
// held for the process lifetime.
type Encryptor struct {
	key []byte // 32 bytes
}

// NewEncryptorFromPassphrase derives a 32-byte master key from a passphrase
// and persistent salt using Argon2id.
func NewEncryptorFromPassphrase(passphrase string, salt []byte) (*Encryptor, error) {
	if passphrase == "" {
		return nil, fmt.Errorf("encryption: passphrase must not be empty")
	}
	if len(salt) == 0 {
		return nil, fmt.Errorf("encryption: salt must not be empty")
	}
	// Argon2id parameters: time=1, memory=64*1024 KiB, threads=4, keyLen=32
	key := argon2.IDKey([]byte(passphrase), salt, 1, 64*1024, 4, 32)
	return &Encryptor{key: key}, nil
}

// NewEncryptorFromKey creates an Encryptor from a pre-existing 32-byte key.
func NewEncryptorFromKey(key []byte) (*Encryptor, error) {
	if len(key) != 32 {
		return nil, fmt.Errorf("encryption: key must be 32 bytes, got %d", len(key))
	}
	keyCopy := make([]byte, 32)
	copy(keyCopy, key)
	return &Encryptor{key: keyCopy}, nil
}

// GenerateKey returns 32 cryptographically random bytes suitable for AES-256.
func GenerateKey() ([]byte, error) {
	key := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, key); err != nil {
		return nil, fmt.Errorf("encryption: generate key: %w", err)
	}
	return key, nil
}

// GenerateSalt returns 16 cryptographically random bytes for Argon2id.
func GenerateSalt() ([]byte, error) {
	salt := make([]byte, 16)
	if _, err := io.ReadFull(rand.Reader, salt); err != nil {
		return nil, fmt.Errorf("encryption: generate salt: %w", err)
	}
	return salt, nil
}

// LoadOrCreateKey loads a base64-encoded key from path, or generates and
// persists a new one if the file does not exist. The parent directory is
// created with 0700 permissions; the key file uses 0600.
func LoadOrCreateKey(path string) ([]byte, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("encryption: create key dir: %w", err)
	}

	data, err := os.ReadFile(path)
	if err == nil {
		// File exists — decode base64.
		key, decErr := base64.StdEncoding.DecodeString(string(data))
		if decErr != nil {
			return nil, fmt.Errorf("encryption: decode key file: %w", decErr)
		}
		if len(key) != 32 {
			return nil, fmt.Errorf("encryption: key file contains %d bytes, want 32", len(key))
		}
		return key, nil
	}
	if !os.IsNotExist(err) {
		return nil, fmt.Errorf("encryption: read key file: %w", err)
	}

	// Generate new key.
	key, err := GenerateKey()
	if err != nil {
		return nil, err
	}
	encoded := base64.StdEncoding.EncodeToString(key)
	if err := os.WriteFile(path, []byte(encoded), 0o600); err != nil {
		return nil, fmt.Errorf("encryption: write key file: %w", err)
	}
	return key, nil
}

// LoadOrCreateSalt loads raw salt bytes from path, or generates and persists
// a new 16-byte salt if the file does not exist. The parent directory is
// created with 0700 permissions; the salt file uses 0600.
func LoadOrCreateSalt(path string) ([]byte, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("encryption: create salt dir: %w", err)
	}

	data, err := os.ReadFile(path)
	if err == nil {
		if len(data) != 16 {
			return nil, fmt.Errorf("encryption: salt file contains %d bytes, want 16", len(data))
		}
		return data, nil
	}
	if !os.IsNotExist(err) {
		return nil, fmt.Errorf("encryption: read salt file: %w", err)
	}

	// Generate new salt.
	salt, err := GenerateSalt()
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(path, salt, 0o600); err != nil {
		return nil, fmt.Errorf("encryption: write salt file: %w", err)
	}
	return salt, nil
}

// Encrypt encrypts plaintext with AES-256-GCM. The returned ciphertext is
// formatted as: nonce (12 bytes) || ciphertext || GCM tag.
// aad may be nil when AAD is not needed (e.g. audit log encryption).
func (e *Encryptor) Encrypt(plaintext []byte, aad []byte) ([]byte, error) {
	block, err := aes.NewCipher(e.key)
	if err != nil {
		return nil, fmt.Errorf("encryption: new cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("encryption: new gcm: %w", err)
	}

	nonce := make([]byte, gcm.NonceSize()) // 12 bytes
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, fmt.Errorf("encryption: generate nonce: %w", err)
	}

	// Seal appends ciphertext+tag to nonce.
	sealed := gcm.Seal(nonce, nonce, plaintext, aad)
	return sealed, nil
}

// Decrypt decrypts ciphertext produced by Encrypt. It validates that the
// input is at least 12 bytes (nonce length) before attempting decryption.
// aad must match the value used during encryption.
func (e *Encryptor) Decrypt(ciphertext []byte, aad []byte) ([]byte, error) {
	if len(ciphertext) < 12 {
		return nil, fmt.Errorf("encryption: ciphertext too short (%d bytes, minimum 12)", len(ciphertext))
	}

	block, err := aes.NewCipher(e.key)
	if err != nil {
		return nil, fmt.Errorf("encryption: new cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("encryption: new gcm: %w", err)
	}

	nonce := ciphertext[:gcm.NonceSize()]
	ct := ciphertext[gcm.NonceSize():]

	plaintext, err := gcm.Open(nil, nonce, ct, aad)
	if err != nil {
		return nil, fmt.Errorf("encryption: decrypt: %w", err)
	}
	return plaintext, nil
}

// BuildMessageAAD constructs the Additional Authenticated Data for a message.
// Uses null byte delimiter — impossible in these string fields, unlike ":"
func BuildMessageAAD(messageID, conversationID, role string) []byte {
	return []byte(messageID + "\x00" + conversationID + "\x00" + role)
}
