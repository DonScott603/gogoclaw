package storage

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestEncryptDecryptRoundTrip(t *testing.T) {
	key, err := GenerateKey()
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	enc, err := NewEncryptorFromKey(key)
	if err != nil {
		t.Fatalf("NewEncryptorFromKey: %v", err)
	}

	plaintext := []byte("hello, world! this is a secret message.")
	ct, err := enc.Encrypt(plaintext, nil)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}

	got, err := enc.Decrypt(ct, nil)
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}
	if !bytes.Equal(got, plaintext) {
		t.Errorf("Decrypt = %q, want %q", got, plaintext)
	}
}

func TestEncryptDecryptWithAAD(t *testing.T) {
	key, err := GenerateKey()
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	enc, err := NewEncryptorFromKey(key)
	if err != nil {
		t.Fatalf("NewEncryptorFromKey: %v", err)
	}

	plaintext := []byte("sensitive content")
	aad := BuildMessageAAD("msg-1", "conv-1", "user")

	ct, err := enc.Encrypt(plaintext, aad)
	if err != nil {
		t.Fatalf("Encrypt with AAD: %v", err)
	}

	// Decrypt with correct AAD succeeds.
	got, err := enc.Decrypt(ct, aad)
	if err != nil {
		t.Fatalf("Decrypt with correct AAD: %v", err)
	}
	if !bytes.Equal(got, plaintext) {
		t.Errorf("Decrypt = %q, want %q", got, plaintext)
	}

	// Decrypt with wrong AAD fails.
	wrongAAD := BuildMessageAAD("msg-2", "conv-1", "user")
	_, err = enc.Decrypt(ct, wrongAAD)
	if err == nil {
		t.Error("expected error when decrypting with wrong AAD")
	}
}

func TestEncryptDifferentNonces(t *testing.T) {
	key, err := GenerateKey()
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	enc, err := NewEncryptorFromKey(key)
	if err != nil {
		t.Fatalf("NewEncryptorFromKey: %v", err)
	}

	plaintext := []byte("same message")
	ct1, err := enc.Encrypt(plaintext, nil)
	if err != nil {
		t.Fatalf("Encrypt 1: %v", err)
	}
	ct2, err := enc.Encrypt(plaintext, nil)
	if err != nil {
		t.Fatalf("Encrypt 2: %v", err)
	}

	if bytes.Equal(ct1, ct2) {
		t.Error("two encryptions of same plaintext should produce different ciphertext")
	}
}

func TestDecryptWrongKey(t *testing.T) {
	key1, _ := GenerateKey()
	key2, _ := GenerateKey()
	enc1, _ := NewEncryptorFromKey(key1)
	enc2, _ := NewEncryptorFromKey(key2)

	plaintext := []byte("secret")
	ct, err := enc1.Encrypt(plaintext, nil)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}

	_, err = enc2.Decrypt(ct, nil)
	if err == nil {
		t.Error("expected error when decrypting with wrong key")
	}
}

func TestDecryptShortInput(t *testing.T) {
	key, _ := GenerateKey()
	enc, _ := NewEncryptorFromKey(key)

	// 11 bytes — shorter than nonce size.
	_, err := enc.Decrypt([]byte("short_input"), nil)
	if err == nil {
		t.Error("expected error for short ciphertext")
	}
}

func TestDecryptEmptyInput(t *testing.T) {
	key, _ := GenerateKey()
	enc, _ := NewEncryptorFromKey(key)

	// Empty input.
	_, err := enc.Decrypt([]byte{}, nil)
	if err == nil {
		t.Error("expected error for empty ciphertext")
	}

	// Nil input.
	_, err = enc.Decrypt(nil, nil)
	if err == nil {
		t.Error("expected error for nil ciphertext")
	}
}

func TestArgon2KeyDerivation(t *testing.T) {
	salt1, err := GenerateSalt()
	if err != nil {
		t.Fatalf("GenerateSalt: %v", err)
	}
	salt2, err := GenerateSalt()
	if err != nil {
		t.Fatalf("GenerateSalt: %v", err)
	}

	passphrase := "my-secret-passphrase"

	enc1, err := NewEncryptorFromPassphrase(passphrase, salt1)
	if err != nil {
		t.Fatalf("NewEncryptorFromPassphrase 1: %v", err)
	}
	enc2, err := NewEncryptorFromPassphrase(passphrase, salt1)
	if err != nil {
		t.Fatalf("NewEncryptorFromPassphrase 2: %v", err)
	}

	// Same passphrase + same salt = same key.
	if !bytes.Equal(enc1.key, enc2.key) {
		t.Error("same passphrase + same salt should produce same key")
	}

	// Same passphrase + different salt = different key.
	enc3, err := NewEncryptorFromPassphrase(passphrase, salt2)
	if err != nil {
		t.Fatalf("NewEncryptorFromPassphrase 3: %v", err)
	}
	if bytes.Equal(enc1.key, enc3.key) {
		t.Error("same passphrase + different salt should produce different key")
	}
}

func TestLoadOrCreateKey(t *testing.T) {
	dir := t.TempDir()
	keyPath := filepath.Join(dir, "data", ".encryption_key")

	// First call creates the file.
	key1, err := LoadOrCreateKey(keyPath)
	if err != nil {
		t.Fatalf("LoadOrCreateKey (create): %v", err)
	}
	if len(key1) != 32 {
		t.Errorf("key length = %d, want 32", len(key1))
	}

	// Verify file permissions.
	info, err := os.Stat(keyPath)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("key file permissions = %o, want 600", perm)
	}

	// Second call loads the existing key.
	key2, err := LoadOrCreateKey(keyPath)
	if err != nil {
		t.Fatalf("LoadOrCreateKey (load): %v", err)
	}
	if !bytes.Equal(key1, key2) {
		t.Error("loaded key should match created key")
	}
}

func TestLoadOrCreateSalt(t *testing.T) {
	dir := t.TempDir()
	saltPath := filepath.Join(dir, "data", ".encryption_salt")

	// First call creates the file.
	salt1, err := LoadOrCreateSalt(saltPath)
	if err != nil {
		t.Fatalf("LoadOrCreateSalt (create): %v", err)
	}
	if len(salt1) != 16 {
		t.Errorf("salt length = %d, want 16", len(salt1))
	}

	// Second call loads the existing salt.
	salt2, err := LoadOrCreateSalt(saltPath)
	if err != nil {
		t.Fatalf("LoadOrCreateSalt (load): %v", err)
	}
	if !bytes.Equal(salt1, salt2) {
		t.Error("loaded salt should match created salt")
	}
}

func TestGenerateKeyLength(t *testing.T) {
	key, err := GenerateKey()
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	if len(key) != 32 {
		t.Errorf("key length = %d, want 32", len(key))
	}
}
