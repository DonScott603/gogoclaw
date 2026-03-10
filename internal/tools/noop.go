package tools

// NoOpSkillLister is a SkillLister that returns no tools. Use it as a safe
// default when the skill system is not configured, eliminating nil checks.
type NoOpSkillLister struct{}

func (NoOpSkillLister) ListSkillTools() []ToolDescriptor { return nil }

// NoOpScrubber is a SecretScrubber that passes text through unchanged.
// Use it as a safe default when no scrubber is configured.
type NoOpScrubber struct{}

func (NoOpScrubber) Scrub(text string) string    { return text }
func (NoOpScrubber) HasSecrets(string) bool       { return false }
