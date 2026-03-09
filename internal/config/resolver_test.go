package config

import (
	"os"
	"testing"
)

func TestResolveEnvVars(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		envKey string
		envVal string
		want   string
	}{
		{
			name:   "resolves single var",
			input:  "api_key: ${MY_API_KEY}",
			envKey: "MY_API_KEY",
			envVal: "secret123",
			want:   "api_key: secret123",
		},
		{
			name:   "resolves multiple vars",
			input:  "${FOO} and ${BAR}",
			envKey: "FOO",
			envVal: "hello",
			want:   "hello and ",
		},
		{
			name:  "unset var resolves to empty",
			input: "key: ${UNSET_VAR_XYZ}",
			want:  "key: ",
		},
		{
			name:  "no vars unchanged",
			input: "plain text",
			want:  "plain text",
		},
		{
			name:  "dollar without braces unchanged",
			input: "cost is $100",
			want:  "cost is $100",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.envKey != "" {
				os.Setenv(tt.envKey, tt.envVal)
				defer os.Unsetenv(tt.envKey)
			}
			got := string(ResolveEnvVars([]byte(tt.input)))
			if got != tt.want {
				t.Errorf("ResolveEnvVars(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}
