package pii

import (
	"regexp"
	"strconv"
	"strings"
)

// PatternType identifies the kind of PII detected.
type PatternType string

const (
	PatternSSN        PatternType = "ssn"
	PatternPhone      PatternType = "phone"
	PatternEmail      PatternType = "email"
	PatternCreditCard PatternType = "credit_card"
	PatternAccount    PatternType = "account_number"
)

// Detection represents a single PII detection result.
type Detection struct {
	Type    PatternType
	Match   string
	Start   int
	End     int
}

// Classifier detects PII patterns in text using regex.
type Classifier struct {
	patterns []compiledPattern
}

type compiledPattern struct {
	typ    PatternType
	re     *regexp.Regexp
	verify func(string) bool // optional post-match validation
}

// NewClassifier creates a Classifier with all built-in patterns.
func NewClassifier() *Classifier {
	return &Classifier{
		patterns: []compiledPattern{
			{typ: PatternSSN, re: regexp.MustCompile(`\b(\d{3}-\d{2}-\d{4})\b`)},
			{typ: PatternSSN, re: regexp.MustCompile(`\b(\d{9})\b`), verify: verifySSN},
			{typ: PatternPhone, re: regexp.MustCompile(`\b(\+?1?[-.\s]?\(?\d{3}\)?[-.\s]?\d{3}[-.\s]?\d{4})\b`)},
			{typ: PatternEmail, re: regexp.MustCompile(`\b([a-zA-Z0-9._%+\-]+@[a-zA-Z0-9.\-]+\.[a-zA-Z]{2,})\b`)},
			{typ: PatternCreditCard, re: regexp.MustCompile(`\b(\d{4}[-\s]?\d{4}[-\s]?\d{4}[-\s]?\d{4})\b`), verify: verifyCreditCard},
			{typ: PatternAccount, re: regexp.MustCompile(`\b(acct?[#:.\s]*\d{6,12})\b`)},
		},
	}
}

// Detect scans text and returns all PII detections found.
func (c *Classifier) Detect(text string) []Detection {
	var detections []Detection
	for _, p := range c.patterns {
		matches := p.re.FindAllStringSubmatchIndex(text, -1)
		for _, loc := range matches {
			if len(loc) < 4 {
				continue
			}
			match := text[loc[2]:loc[3]]
			if p.verify != nil && !p.verify(match) {
				continue
			}
			detections = append(detections, Detection{
				Type:  p.typ,
				Match: match,
				Start: loc[2],
				End:   loc[3],
			})
		}
	}
	return detections
}

// HasPII returns true if any PII patterns are found in text.
func (c *Classifier) HasPII(text string) bool {
	for _, p := range c.patterns {
		matches := p.re.FindAllStringSubmatch(text, 1)
		for _, m := range matches {
			if len(m) < 2 {
				continue
			}
			if p.verify == nil || p.verify(m[1]) {
				return true
			}
		}
	}
	return false
}

// PatternTypes returns the distinct set of pattern types detected.
func PatternTypes(detections []Detection) []string {
	seen := make(map[PatternType]bool)
	var types []string
	for _, d := range detections {
		if !seen[d.Type] {
			seen[d.Type] = true
			types = append(types, string(d.Type))
		}
	}
	return types
}

// verifySSN checks that a 9-digit number looks like a valid SSN.
func verifySSN(s string) bool {
	if len(s) != 9 {
		return false
	}
	// Area number (first 3) can't be 000, 666, or 900-999.
	area, _ := strconv.Atoi(s[:3])
	if area == 0 || area == 666 || area >= 900 {
		return false
	}
	// Group number (middle 2) can't be 00.
	group, _ := strconv.Atoi(s[3:5])
	if group == 0 {
		return false
	}
	// Serial (last 4) can't be 0000.
	serial, _ := strconv.Atoi(s[5:])
	return serial != 0
}

// verifyCreditCard uses the Luhn algorithm to validate credit card numbers.
func verifyCreditCard(s string) bool {
	// Strip separators.
	digits := strings.Map(func(r rune) rune {
		if r >= '0' && r <= '9' {
			return r
		}
		return -1
	}, s)

	if len(digits) < 13 || len(digits) > 19 {
		return false
	}

	return luhnValid(digits)
}

func luhnValid(digits string) bool {
	sum := 0
	alt := false
	for i := len(digits) - 1; i >= 0; i-- {
		n := int(digits[i] - '0')
		if alt {
			n *= 2
			if n > 9 {
				n -= 9
			}
		}
		sum += n
		alt = !alt
	}
	return sum%10 == 0
}
