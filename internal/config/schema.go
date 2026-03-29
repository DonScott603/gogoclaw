package config

import "time"

// Config is the top-level application configuration, assembled from
// multiple YAML files under ~/.gogoclaw/.
type Config struct {
	Workspace WorkspaceConfig          `yaml:"workspace" json:"workspace"`
	Logging   LoggingConfig            `yaml:"logging" json:"logging"`
	Storage   StorageConfig            `yaml:"storage" json:"storage"`
	Providers map[string]ProviderConfig `yaml:"providers" json:"providers"`
	Agents    map[string]AgentConfig   `yaml:"agents" json:"agents"`
	Channels  map[string]ChannelConfig `yaml:"channels" json:"channels"`
	Network   NetworkConfig              `yaml:"network" json:"network"`
	Memory    MemoryStoreConfig          `yaml:"memory" json:"memory"`
	MCP       map[string]MCPServerConfig `yaml:"mcp" json:"mcp"`
}

// WorkspaceConfig defines workspace directory paths.
type WorkspaceConfig struct {
	Base      string `yaml:"base" json:"base"`
	Inbox     string `yaml:"inbox" json:"inbox"`
	Outbox    string `yaml:"outbox" json:"outbox"`
	Scratch   string `yaml:"scratch" json:"scratch"`
	Documents string `yaml:"documents" json:"documents"`
}

// LoggingConfig controls log output and audit trail settings.
type LoggingConfig struct {
	Level string      `yaml:"level" json:"level"`
	Audit AuditConfig `yaml:"audit" json:"audit"`
}

// AuditConfig controls the structured audit log.
type AuditConfig struct {
	Enabled bool   `yaml:"enabled" json:"enabled"`
	Path    string `yaml:"path" json:"path"`
	Encrypt bool   `yaml:"encrypt" json:"encrypt"`
}

// StorageConfig controls conversation persistence.
type StorageConfig struct {
	Conversations ConversationStorageConfig `yaml:"conversations" json:"conversations"`
	EphemeralMode bool                      `yaml:"ephemeral_mode" json:"ephemeral_mode"`
}

// ConversationStorageConfig defines SQLite storage parameters.
type ConversationStorageConfig struct {
	Path          string `yaml:"path" json:"path"`
	Encrypt       bool   `yaml:"encrypt" json:"encrypt"`       // TODO: Not yet implemented. Reserved for future use.
	PassphraseEnv string `yaml:"passphrase_env" json:"passphrase_env"`
}

// ProviderConfig defines an LLM provider connection.
type ProviderConfig struct {
	Name             string        `yaml:"name" json:"name"`
	Type             string        `yaml:"type" json:"type"`
	BaseURL          string        `yaml:"base_url" json:"base_url"`
	APIKey           string        `yaml:"api_key" json:"api_key"`
	DefaultModel     string        `yaml:"default_model" json:"default_model"`
	MaxContextTokens int           `yaml:"max_context_tokens" json:"max_context_tokens"`
	Timeout          time.Duration `yaml:"timeout" json:"timeout"`
	Retry            int           `yaml:"retry" json:"retry"`
}

// AgentConfig defines an agent profile.
type AgentConfig struct {
	Name             string              `yaml:"name" json:"name"`
	Inherits         string              `yaml:"inherits,omitempty" json:"inherits,omitempty"`
	SystemPromptFile string              `yaml:"system_prompt_file" json:"system_prompt_file"`
	ProviderRouting  ProviderRouting     `yaml:"provider_routing" json:"provider_routing"`
	PII              PIIConfig           `yaml:"pii" json:"pii"`
	Skills           AgentSkillsConfig   `yaml:"skills" json:"skills"`
	Context          AgentContextConfig  `yaml:"context" json:"context"`
	MemoryConfig     AgentMemoryConfig   `yaml:"memory" json:"memory"`
	Shell            ShellConfig         `yaml:"shell" json:"shell"`
	Network          AgentNetworkConfig  `yaml:"network,omitempty" json:"network,omitempty"`
}

// ProviderRouting controls how the agent selects LLM providers.
type ProviderRouting struct {
	Mode          string          `yaml:"mode" json:"mode"`
	ProviderChain []ProviderEntry `yaml:"provider_chain" json:"provider_chain"`
}

// ProviderEntry is a single entry in the provider chain.
type ProviderEntry struct {
	Provider string        `yaml:"provider" json:"provider"`
	Timeout  time.Duration `yaml:"timeout" json:"timeout"`
	Retry    int           `yaml:"retry" json:"retry"`
}

// PIIConfig controls PII detection mode.
type PIIConfig struct {
	Mode string `yaml:"mode" json:"mode"`
}

// AgentSkillsConfig controls skill availability.
type AgentSkillsConfig struct {
	AlwaysAvailable bool     `yaml:"always_available" json:"always_available"`
	AutoDiscover    bool     `yaml:"auto_discover" json:"auto_discover"`
	Allowed         []string `yaml:"allowed,omitempty" json:"allowed,omitempty"`
}

// AgentContextConfig controls context assembly.
type AgentContextConfig struct {
	MaxHistoryTokens int                   `yaml:"max_history_tokens" json:"max_history_tokens"`
	Summarization    SummarizationConfig   `yaml:"summarization" json:"summarization"`
}

// SummarizationConfig controls rolling summarization.
type SummarizationConfig struct {
	Enabled         bool   `yaml:"enabled" json:"enabled"`
	Provider        string `yaml:"provider,omitempty" json:"provider,omitempty"`
	ThresholdTokens int    `yaml:"threshold_tokens" json:"threshold_tokens"`
}

// AgentMemoryConfig controls per-agent memory retrieval.
type AgentMemoryConfig struct {
	Enabled            bool    `yaml:"enabled" json:"enabled"`
	TopK               int     `yaml:"top_k" json:"top_k"`
	RelevanceThreshold float64 `yaml:"relevance_threshold" json:"relevance_threshold"`
	RecencyWeight      float64 `yaml:"recency_weight" json:"recency_weight"`
}

// ShellConfig controls shell execution confirmation.
type ShellConfig struct {
	Confirmation string `yaml:"confirmation" json:"confirmation"`
}

// AgentNetworkConfig allows per-agent network allowlist additions.
type AgentNetworkConfig struct {
	AdditionalAllowlist []string `yaml:"additional_allowlist,omitempty" json:"additional_allowlist,omitempty"`
}

// ChannelConfig defines a communication channel.
type ChannelConfig struct {
	Name           string        `yaml:"name" json:"name"`
	Enabled        bool          `yaml:"enabled" json:"enabled"`
	TokenEnv       string        `yaml:"token_env,omitempty" json:"token_env,omitempty"`
	AllowedUsers   []string      `yaml:"allowed_users,omitempty" json:"allowed_users,omitempty"`
	PollingTimeout time.Duration `yaml:"polling_timeout,omitempty" json:"polling_timeout,omitempty"`
	Listen         string        `yaml:"listen,omitempty" json:"listen,omitempty"`
	APIKey         string        `yaml:"api_key,omitempty" json:"api_key,omitempty"`
	APIKeyEnv      string        `yaml:"api_key_env,omitempty" json:"api_key_env,omitempty"`
	AllowedOrigins []string      `yaml:"allowed_origins,omitempty" json:"allowed_origins,omitempty"`
}

// NetworkConfig defines the global network allowlist.
type NetworkConfig struct {
	Allowlist      []string `yaml:"allowlist" json:"allowlist"`
	DenyAllOthers  bool     `yaml:"deny_all_others" json:"deny_all_others"`
	LogBlocked     bool     `yaml:"log_blocked" json:"log_blocked"`
}

// MemoryStoreConfig controls the vector memory backend.
type MemoryStoreConfig struct {
	Enabled   bool                `yaml:"enabled" json:"enabled"`
	Embedding EmbeddingConfig     `yaml:"embedding" json:"embedding"`
	Storage   MemoryBackendConfig `yaml:"storage" json:"storage"`
	Retrieval RetrievalConfig     `yaml:"retrieval" json:"retrieval"`
}

// EmbeddingConfig defines which provider generates embeddings.
type EmbeddingConfig struct {
	Provider         string `yaml:"provider" json:"provider"`
	Model            string `yaml:"model" json:"model"`
	FallbackProvider string `yaml:"fallback_provider,omitempty" json:"fallback_provider,omitempty"`
}

// MemoryBackendConfig defines vector storage parameters.
type MemoryBackendConfig struct {
	Backend   string `yaml:"backend" json:"backend"`
	Path      string `yaml:"path" json:"path"`
	Encrypted bool   `yaml:"encrypted" json:"encrypted"`
}

// RetrievalConfig controls memory search behavior.
type RetrievalConfig struct {
	TopK               int     `yaml:"top_k" json:"top_k"`
	RelevanceThreshold float64 `yaml:"relevance_threshold" json:"relevance_threshold"`
	RecencyWeight      float64 `yaml:"recency_weight" json:"recency_weight"`
}

// MCPServerConfig defines an MCP (Model Context Protocol) server connection.
type MCPServerConfig struct {
	Name      string   `yaml:"name" json:"name"`
	Transport string   `yaml:"transport" json:"transport"` // "stdio" or "sse"
	Command   string   `yaml:"command,omitempty" json:"command,omitempty"`
	Args      []string `yaml:"args,omitempty" json:"args,omitempty"`
	URL       string   `yaml:"url,omitempty" json:"url,omitempty"`
	Enabled   bool     `yaml:"enabled" json:"enabled"`
}
