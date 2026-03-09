package security

import (
	"strings"
	"testing"
)

func TestSecretScrubberOpenAIKey(t *testing.T) {
	s := NewSecretScrubber()
	text := "My key is sk-abcdefghij1234567890abcdefghij1234567890"
	result := s.Scrub(text)
	if strings.Contains(result, "abcdefghij1234567890") {
		t.Errorf("scrubbed text still contains key: %s", result)
	}
	if !strings.Contains(result, "sk-a[REDACTED]") {
		t.Errorf("expected partial key prefix, got: %s", result)
	}
}

func TestSecretScrubberBearerToken(t *testing.T) {
	s := NewSecretScrubber()
	text := "Authorization: Bearer eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.test"
	result := s.Scrub(text)
	if strings.Contains(result, "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9") {
		t.Errorf("scrubbed text still contains bearer token: %s", result)
	}
}

func TestSecretScrubberAPIKeyPattern(t *testing.T) {
	s := NewSecretScrubber()
	text := `config: api_key=abcdef1234567890abcdef1234567890`
	result := s.Scrub(text)
	if !strings.Contains(result, "[REDACTED]") {
		t.Errorf("expected REDACTED in result: %s", result)
	}
}

func TestSecretScrubberNoSecrets(t *testing.T) {
	s := NewSecretScrubber()
	text := "This is a normal message with no secrets."
	result := s.Scrub(text)
	if result != text {
		t.Errorf("clean text should not be modified: %s", result)
	}
}

func TestSecretScrubberHasSecrets(t *testing.T) {
	s := NewSecretScrubber()
	if !s.HasSecrets("key: sk-abcdefghij1234567890abcdefghij") {
		t.Error("HasSecrets should detect sk- pattern")
	}
	if s.HasSecrets("no secrets here") {
		t.Error("HasSecrets should return false for clean text")
	}
}

func TestSecretScrubberScrubMap(t *testing.T) {
	s := NewSecretScrubber()
	m := map[string]string{
		"api_key":  "my-secret-value",
		"name":     "normal-value",
		"password": "hunter2",
	}
	result := s.ScrubMap(m)
	if result["api_key"] != "[REDACTED]" {
		t.Errorf("api_key should be REDACTED, got %q", result["api_key"])
	}
	if result["password"] != "[REDACTED]" {
		t.Errorf("password should be REDACTED, got %q", result["password"])
	}
	if result["name"] != "normal-value" {
		t.Errorf("name should be unchanged, got %q", result["name"])
	}
}

func TestSecretScrubberAWSKey(t *testing.T) {
	s := NewSecretScrubber()
	text := "AWS key: AKIAIOSFODNN7EXAMPLE"
	result := s.Scrub(text)
	if !strings.Contains(result, "[REDACTED]") {
		t.Errorf("AWS key should be redacted: %s", result)
	}
}
