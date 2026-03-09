package security

import (
	"regexp"
	"strings"
)

// SecretScrubber detects and redacts potential secrets from text.
type SecretScrubber struct {
	patterns []*regexp.Regexp
}

// NewSecretScrubber creates a scrubber with built-in secret patterns.
func NewSecretScrubber() *SecretScrubber {
	return &SecretScrubber{
		patterns: []*regexp.Regexp{
			// OpenAI / Anthropic style keys.
			regexp.MustCompile(`\b(sk-[a-zA-Z0-9]{20,})\b`),
			// Bearer tokens.
			regexp.MustCompile(`(?i)(Bearer\s+[a-zA-Z0-9\-_.~+/]{20,})`),
			// Generic API key patterns (key= or api_key= followed by long alphanumeric).
			regexp.MustCompile(`(?i)((?:api[_-]?key|apikey|secret[_-]?key|access[_-]?token)\s*[=:]\s*["\']?[a-zA-Z0-9\-_.]{16,}["\']?)`),
			// AWS access keys.
			regexp.MustCompile(`\b(AKIA[0-9A-Z]{16})\b`),
			// Generic long hex/base64 tokens (40+ chars).
			regexp.MustCompile(`\b([a-fA-F0-9]{40,})\b`),
		},
	}
}

// Scrub replaces detected secrets with [REDACTED].
func (s *SecretScrubber) Scrub(text string) string {
	result := text
	for _, p := range s.patterns {
		result = p.ReplaceAllStringFunc(result, func(match string) string {
			// Keep first 4 chars for identification.
			if len(match) > 8 {
				return match[:4] + "[REDACTED]"
			}
			return "[REDACTED]"
		})
	}
	return result
}

// HasSecrets returns true if the text contains potential secrets.
func (s *SecretScrubber) HasSecrets(text string) bool {
	for _, p := range s.patterns {
		if p.MatchString(text) {
			return true
		}
	}
	return false
}

// ScrubMessages scrubs secrets from a slice of message contents.
func (s *SecretScrubber) ScrubMessages(contents []string) []string {
	out := make([]string, len(contents))
	for i, c := range contents {
		out[i] = s.Scrub(c)
	}
	return out
}

// ScrubMap scrubs secret values from a string map (e.g., metadata).
func (s *SecretScrubber) ScrubMap(m map[string]string) map[string]string {
	out := make(map[string]string, len(m))
	for k, v := range m {
		lk := strings.ToLower(k)
		if strings.Contains(lk, "key") || strings.Contains(lk, "secret") ||
			strings.Contains(lk, "token") || strings.Contains(lk, "password") {
			out[k] = "[REDACTED]"
		} else {
			out[k] = s.Scrub(v)
		}
	}
	return out
}
