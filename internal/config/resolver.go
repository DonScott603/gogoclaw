package config

import (
	"os"
	"regexp"
)

var envVarPattern = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)\}`)

// ResolveEnvVars replaces ${VAR_NAME} patterns in a byte slice with
// the corresponding environment variable values. Unset variables resolve
// to empty strings.
func ResolveEnvVars(data []byte) []byte {
	return envVarPattern.ReplaceAllFunc(data, func(match []byte) []byte {
		submatch := envVarPattern.FindSubmatch(match)
		if len(submatch) < 2 {
			return match
		}
		return []byte(os.Getenv(string(submatch[1])))
	})
}
