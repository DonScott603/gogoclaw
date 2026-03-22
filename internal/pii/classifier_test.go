package pii

import (
	"testing"
)

func TestDetectSSNDashed(t *testing.T) {
	c := NewClassifier()
	detections := c.Detect("My SSN is 123-45-6789 please process it.")
	found := false
	for _, d := range detections {
		if d.Type == PatternSSN && d.Match == "123-45-6789" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected SSN detection for 123-45-6789, got %v", detections)
	}
}

func TestDetectSSNRaw(t *testing.T) {
	c := NewClassifier()
	detections := c.Detect("SSN: 123456789")
	found := false
	for _, d := range detections {
		if d.Type == PatternSSN {
			found = true
		}
	}
	if !found {
		t.Errorf("expected SSN detection for 123456789, got %v", detections)
	}
}

func TestDetectSSNInvalid(t *testing.T) {
	c := NewClassifier()
	// 000 area is invalid.
	detections := c.Detect("Number: 000123456")
	for _, d := range detections {
		if d.Type == PatternSSN {
			t.Errorf("should not detect invalid SSN 000123456, got %v", d)
		}
	}
}

func TestDetectPhone(t *testing.T) {
	tests := []struct {
		input string
		want  bool
	}{
		{"Call me at 555-123-4567", true},
		{"Phone: (555) 123-4567", true},
		{"1-800-555-1234", true},
		{"no phone here", false},
	}
	c := NewClassifier()
	for _, tt := range tests {
		detections := c.Detect(tt.input)
		found := false
		for _, d := range detections {
			if d.Type == PatternPhone {
				found = true
			}
		}
		if found != tt.want {
			t.Errorf("Detect(%q) phone found=%v, want %v", tt.input, found, tt.want)
		}
	}
}

func TestDetectEmail(t *testing.T) {
	c := NewClassifier()
	detections := c.Detect("Contact user@example.com for details")
	found := false
	for _, d := range detections {
		if d.Type == PatternEmail && d.Match == "user@example.com" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected email detection, got %v", detections)
	}
}

func TestDetectEmailNone(t *testing.T) {
	c := NewClassifier()
	detections := c.Detect("no email in this text")
	for _, d := range detections {
		if d.Type == PatternEmail {
			t.Errorf("should not detect email, got %v", d)
		}
	}
}

func TestDetectCreditCard(t *testing.T) {
	c := NewClassifier()
	// Valid Luhn: 4532015112830366.
	detections := c.Detect("Card: 4532 0151 1283 0366")
	found := false
	for _, d := range detections {
		if d.Type == PatternCreditCard {
			found = true
		}
	}
	if !found {
		t.Errorf("expected credit card detection, got %v", detections)
	}
}

func TestDetectCreditCardInvalidLuhn(t *testing.T) {
	c := NewClassifier()
	// Invalid Luhn.
	detections := c.Detect("Card: 1234 5678 9012 3456")
	for _, d := range detections {
		if d.Type == PatternCreditCard {
			t.Errorf("should not detect invalid credit card, got %v", d)
		}
	}
}

func TestDetectAccountNumber(t *testing.T) {
	c := NewClassifier()
	detections := c.Detect("My acct# 12345678 at the bank")
	found := false
	for _, d := range detections {
		if d.Type == PatternAccount {
			found = true
		}
	}
	if !found {
		t.Errorf("expected account number detection, got %v", detections)
	}
}

func TestHasPII(t *testing.T) {
	c := NewClassifier()
	if !c.HasPII("SSN: 123-45-6789") {
		t.Error("HasPII should return true for SSN")
	}
	if c.HasPII("nothing sensitive here") {
		t.Error("HasPII should return false for clean text")
	}
}

func TestPatternTypes(t *testing.T) {
	detections := []Detection{
		{Type: PatternSSN},
		{Type: PatternEmail},
		{Type: PatternSSN}, // duplicate
	}
	types := PatternTypes(detections)
	if len(types) != 2 {
		t.Errorf("expected 2 unique types, got %d: %v", len(types), types)
	}
}

func TestDetectAPIKey(t *testing.T) {
	c := NewClassifier()
	detections := c.Detect("My key is sk-abc123def456ghi789jkl012mno345")
	found := false
	for _, d := range detections {
		if d.Type == PatternAPIKey {
			found = true
		}
	}
	if !found {
		t.Errorf("expected API key detection, got %v", detections)
	}
}

func TestDetectGitHubToken(t *testing.T) {
	c := NewClassifier()
	detections := c.Detect("Token: ghp_ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghij")
	found := false
	for _, d := range detections {
		if d.Type == PatternAPIKey {
			found = true
		}
	}
	if !found {
		t.Errorf("expected GitHub token detection, got %v", detections)
	}
}

func TestDetectBearerToken(t *testing.T) {
	c := NewClassifier()
	detections := c.Detect("Authorization: Bearer eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9")
	found := false
	for _, d := range detections {
		if d.Type == PatternBearer {
			found = true
		}
	}
	if !found {
		t.Errorf("expected Bearer token detection, got %v", detections)
	}
}

func TestDetectAWSKey(t *testing.T) {
	c := NewClassifier()
	detections := c.Detect("aws_access_key_id = AKIAIOSFODNN7EXAMPLE")
	found := false
	for _, d := range detections {
		if d.Type == PatternAWSKey {
			found = true
		}
	}
	if !found {
		t.Errorf("expected AWS key detection, got %v", detections)
	}
}

func TestNoSecretsFalsePositive(t *testing.T) {
	c := NewClassifier()
	detections := c.Detect("The sky is blue and the grass is green.")
	for _, d := range detections {
		if d.Type == PatternAPIKey || d.Type == PatternBearer || d.Type == PatternAWSKey {
			t.Errorf("should not detect secrets in clean text, got %v", d)
		}
	}
}

func TestLuhnValid(t *testing.T) {
	tests := []struct {
		digits string
		want   bool
	}{
		{"4532015112830366", true},
		{"79927398713", true},
		{"1234567890123456", false},
	}
	for _, tt := range tests {
		got := luhnValid(tt.digits)
		if got != tt.want {
			t.Errorf("luhnValid(%q) = %v, want %v", tt.digits, got, tt.want)
		}
	}
}

func TestVerifySSN(t *testing.T) {
	tests := []struct {
		ssn  string
		want bool
	}{
		{"123456789", true},
		{"000123456", false}, // area 000
		{"666123456", false}, // area 666
		{"900123456", false}, // area >= 900
		{"123004567", false}, // group 00
		{"123450000", false}, // serial 0000
	}
	for _, tt := range tests {
		got := verifySSN(tt.ssn)
		if got != tt.want {
			t.Errorf("verifySSN(%q) = %v, want %v", tt.ssn, got, tt.want)
		}
	}
}
