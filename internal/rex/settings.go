package rex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"sync"

	"github.com/zalando/go-keyring"
)

// Secrets stores credentials outside SQLite. The production implementation
// is the macOS Keychain; tests use an in-memory map.
type Secrets interface {
	Get(key string) (string, error)
	Set(key, value string) error
	Delete(key string) error
}

type KeychainSecrets struct{ Service string }

func (k KeychainSecrets) Get(key string) (string, error) {
	value, err := keyring.Get(k.Service, key)
	if errors.Is(err, keyring.ErrNotFound) {
		return "", nil
	}
	return value, err
}

func (k KeychainSecrets) Set(key, value string) error { return keyring.Set(k.Service, key, value) }

func (k KeychainSecrets) Delete(key string) error {
	err := keyring.Delete(k.Service, key)
	if errors.Is(err, keyring.ErrNotFound) {
		return nil
	}
	return err
}

type MemorySecrets struct {
	mu     sync.Mutex
	values map[string]string
}

func NewMemorySecrets() *MemorySecrets { return &MemorySecrets{values: map[string]string{}} }

func (m *MemorySecrets) Get(key string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.values[key], nil
}

func (m *MemorySecrets) Set(key, value string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.values[key] = value
	return nil
}

func (m *MemorySecrets) Delete(key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.values, key)
	return nil
}

// ProviderConfig is one model provider's non-secret settings plus a flag
// telling the UI whether a key is stored. Keys are write-only from the UI.
type ProviderConfig struct {
	BaseURL    string `json:"base_url"`
	Configured bool   `json:"configured"`
	APIKey     string `json:"api_key,omitempty"`
	ClearKey   bool   `json:"clear_key,omitempty"`
}

type MemorySettings struct {
	Enabled         bool   `json:"enabled"`
	Provider        string `json:"provider"`
	ExtractionModel string `json:"extraction_model"`
	EmbeddingModel  string `json:"embedding_model"`
	// EmbeddingSource is "bundled" (Rex's own Bekko Embedding, the
	// default when empty) or "server" (EmbeddingModel on the memory provider).
	EmbeddingSource string `json:"embedding_source"`
}

type Settings struct {
	Local           ProviderConfig `json:"local"`
	Anthropic       ProviderConfig `json:"anthropic"`
	OpenAI          ProviderConfig `json:"openai"`
	OpenRouter      ProviderConfig `json:"openrouter"`
	DefaultProvider string         `json:"default_provider"`
	DefaultModel    string         `json:"default_model"`
	Memory          MemorySettings `json:"memory"`
	BrowserEnabled  bool           `json:"browser_enabled"`
	// BrowserPath is the Chromium-based browser bots use ("" picks the first
	// installed one); BrowserHidden runs bot browsers without a window.
	BrowserPath   string `json:"browser_path"`
	BrowserHidden bool   `json:"browser_hidden"`
	// TypeSafe reviews proposed commands and file edits (jev). Its key is
	// stored in the Keychain; auto-approve lets it clear low-risk local steps.
	TypeSafe            ProviderConfig `json:"typesafe"`
	TypeSafeAutoApprove bool           `json:"typesafe_auto_approve"`
	// Web search providers for the runtime's web_search tool. Only keys
	// are stored (in the Keychain); TinyFish is tried first, then Exa,
	// then Brave.
	TinyFish ProviderConfig `json:"tinyfish"`
	Exa      ProviderConfig `json:"exa"`
	Brave    ProviderConfig `json:"brave"`
	// NotificationsOff stops system notifications when a bot needs the user.
	NotificationsOff bool `json:"notifications_off"`
	Onboarded        bool `json:"onboarded"`
}

const settingsKey = "settings.v1"

const providerTypeSafe = "typesafe"

// Web search providers, in the order the runtime tries them.
const (
	searchTinyFish = "tinyfish"
	searchExa      = "exa"
	searchBrave    = "brave"
)

// SettingsManager persists Settings in SQLite and API keys in Secrets.
type SettingsManager struct {
	store   *Store
	secrets Secrets
	mu      sync.Mutex
}

func NewSettingsManager(store *Store, secrets Secrets) *SettingsManager {
	return &SettingsManager{store: store, secrets: secrets}
}

func defaultSettings() Settings {
	return Settings{
		Local:           ProviderConfig{BaseURL: "http://127.0.0.1:1234/v1"},
		DefaultProvider: ProviderLocal,
		Memory:          MemorySettings{Enabled: true, Provider: ProviderLocal},
		BrowserEnabled:  true,
	}
}

func secretKey(provider string) string { return "provider." + provider + ".api_key" }

// Get returns settings without secrets.
func (m *SettingsManager) Get(ctx context.Context) (Settings, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.load(ctx)
}

func (m *SettingsManager) load(ctx context.Context) (Settings, error) {
	settings := defaultSettings()
	raw, err := m.store.GetSetting(ctx, settingsKey)
	if err != nil {
		return settings, err
	}
	if raw != "" {
		if err := json.Unmarshal([]byte(raw), &settings); err != nil {
			return settings, fmt.Errorf("decode settings: %w", err)
		}
	}
	for name, config := range settings.providers() {
		key, err := m.secrets.Get(secretKey(name))
		if err != nil {
			return settings, fmt.Errorf("read %s key from keychain: %w", name, err)
		}
		config.Configured = key != ""
		config.APIKey = ""
		config.ClearKey = false
	}
	return settings, nil
}

func (s *Settings) providers() map[string]*ProviderConfig {
	return map[string]*ProviderConfig{ProviderLocal: &s.Local, ProviderAnthropic: &s.Anthropic, ProviderOpenAI: &s.OpenAI, ProviderOpenRouter: &s.OpenRouter, providerTypeSafe: &s.TypeSafe,
		searchTinyFish: &s.TinyFish, searchExa: &s.Exa, searchBrave: &s.Brave}
}

// WebSearchReady reports whether any web search provider has a key, which
// is what makes the runtime offer web_search.
func (s Settings) WebSearchReady() bool {
	return s.TinyFish.Configured || s.Exa.Configured || s.Brave.Configured
}

// APIKey reads one provider key from the keychain.
func (m *SettingsManager) APIKey(provider string) (string, error) {
	return m.secrets.Get(secretKey(provider))
}

// Save validates and stores settings. Keys present in the input are written
// to the keychain; empty keys keep the stored value unless ClearKey is set.
// It reports whether the runtime must restart to pick up the change.
func (m *SettingsManager) Save(ctx context.Context, input Settings) (Settings, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	previous, err := m.load(ctx)
	if err != nil {
		return previous, false, err
	}
	for name, config := range input.providers() {
		config.BaseURL = strings.TrimRight(strings.TrimSpace(config.BaseURL), "/")
		if config.BaseURL != "" {
			if err := validateEndpointURL(config.BaseURL); err != nil {
				return previous, false, fmt.Errorf("%s base URL: %w", name, err)
			}
		}
	}
	if input.Local.BaseURL == "" {
		input.Local.BaseURL = defaultSettings().Local.BaseURL
	}
	switch input.DefaultProvider {
	case ProviderLocal, ProviderAnthropic, ProviderOpenAI, ProviderOpenRouter:
	default:
		input.DefaultProvider = ProviderLocal
	}
	switch input.Memory.Provider {
	case ProviderLocal, ProviderOpenAI:
	default:
		input.Memory.Provider = ProviderLocal
	}
	keysChanged := false
	for name, config := range input.providers() {
		key := strings.TrimSpace(config.APIKey)
		switch {
		case config.ClearKey:
			if err := m.secrets.Delete(secretKey(name)); err != nil {
				return previous, false, err
			}
			keysChanged = true
		case key != "":
			if err := m.secrets.Set(secretKey(name), key); err != nil {
				return previous, false, fmt.Errorf("store %s key in keychain: %w", name, err)
			}
			keysChanged = true
		}
		config.APIKey, config.ClearKey, config.Configured = "", false, false
	}
	payload, err := json.Marshal(input)
	if err != nil {
		return previous, false, err
	}
	if err := m.store.SetSetting(ctx, settingsKey, string(payload)); err != nil {
		return previous, false, err
	}
	saved, err := m.load(ctx)
	restart := keysChanged || saved.Local.BaseURL != previous.Local.BaseURL || saved.Anthropic.BaseURL != previous.Anthropic.BaseURL ||
		saved.OpenAI.BaseURL != previous.OpenAI.BaseURL || saved.OpenRouter.BaseURL != previous.OpenRouter.BaseURL ||
		saved.BrowserEnabled != previous.BrowserEnabled || saved.BrowserPath != previous.BrowserPath || saved.TypeSafeAutoApprove != previous.TypeSafeAutoApprove
	return saved, restart, err
}

func validateEndpointURL(raw string) error {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" {
		return errors.New("must be an absolute URL")
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return errors.New("must use http or https")
	}
	if parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return errors.New("must not contain credentials, a query, or a fragment")
	}
	return nil
}

// RuntimeEnv returns provider environment for the agent-runtime process.
func (m *SettingsManager) RuntimeEnv(ctx context.Context) ([]string, error) {
	settings, err := m.Get(ctx)
	if err != nil {
		return nil, err
	}
	env := []string{"AGENT_RUNTIME_NATIVE_EINO=true"}
	if key, err := m.APIKey(providerTypeSafe); err != nil {
		return nil, err
	} else if key != "" {
		env = append(env, "TYPESAFE_API_KEY="+key, "AGENT_RUNTIME_TYPESAFE_ENABLED=true",
			"AGENT_RUNTIME_TYPESAFE_AUTO_APPROVE="+fmt.Sprint(settings.TypeSafeAutoApprove))
	}
	for _, item := range []struct{ provider, keyEnv string }{
		{searchTinyFish, "TINYFISH_API_KEY"}, {searchExa, "EXA_API_KEY"}, {searchBrave, "BRAVE_SEARCH_API_KEY"},
	} {
		key, err := m.APIKey(item.provider)
		if err != nil {
			return nil, err
		}
		if key != "" {
			env = append(env, item.keyEnv+"="+key)
		}
	}
	for _, item := range []struct{ provider, keyEnv, urlEnv, baseURL string }{
		{ProviderAnthropic, "ANTHROPIC_API_KEY", "ANTHROPIC_BASE_URL", settings.Anthropic.BaseURL},
		{ProviderOpenAI, "OPENAI_API_KEY", "OPENAI_BASE_URL", settings.OpenAI.BaseURL},
		{ProviderOpenRouter, "OPENROUTER_API_KEY", "OPENROUTER_BASE_URL", settings.OpenRouter.BaseURL},
	} {
		key, err := m.APIKey(item.provider)
		if err != nil {
			return nil, err
		}
		if key != "" {
			env = append(env, item.keyEnv+"="+key)
		}
		if item.baseURL != "" {
			env = append(env, item.urlEnv+"="+item.baseURL)
		}
	}
	return env, nil
}

// LocalEndpoint is the approved runtime binding for the local model server.
func (s Settings) LocalEndpoint() ModelEndpoint {
	mode := "none"
	if s.Local.Configured {
		mode = "api_key"
	}
	return ModelEndpoint{ID: "local", BaseURL: s.Local.BaseURL, AuthMode: mode}
}

// RunModelFor selects the model and credential for one run of a bot.
func (m *SettingsManager) RunModelFor(ctx context.Context, bot Bot) (*RunModel, *ModelCredential, error) {
	settings, err := m.Get(ctx)
	if err != nil {
		return nil, nil, err
	}
	model := bot.Model
	if model == "" && bot.Provider == settings.DefaultProvider {
		model = settings.DefaultModel
	}
	if bot.Provider != ProviderLocal {
		if !settings.providers()[bot.Provider].Configured {
			return nil, nil, fmt.Errorf("%s has no API key yet. Add one in Settings → Models", providerLabel(bot.Provider))
		}
		if model == "" {
			return nil, nil, nil
		}
		return &RunModel{Provider: bot.RuntimeProvider(), Model: model}, nil, nil
	}
	if model == "" {
		return nil, nil, errors.New("choose a model for this bot (Settings → Models lists the models your local server offers)")
	}
	endpoint := settings.LocalEndpoint()
	credential := &ModelCredential{Type: "none", ConnectionID: "local"}
	if endpoint.AuthMode == "api_key" {
		key, err := m.APIKey(ProviderLocal)
		if err != nil {
			return nil, nil, err
		}
		credential = &ModelCredential{Type: "api_key", APIKey: key, ConnectionID: "local"}
	}
	return &RunModel{Provider: "openai_compatible", Model: model, Endpoint: &endpoint}, credential, nil
}

func providerLabel(provider string) string {
	switch provider {
	case ProviderAnthropic:
		return "Anthropic"
	case ProviderOpenAI:
		return "OpenAI"
	case ProviderOpenRouter:
		return "OpenRouter"
	default:
		return "The local model server"
	}
}

// FileSecrets keeps secrets in a 0600 JSON file. It exists for automated
// tests and development only; the app uses the Keychain.
type FileSecrets struct {
	Path string
	mu   sync.Mutex
}

func (f *FileSecrets) read() map[string]string {
	values := map[string]string{}
	if payload, err := os.ReadFile(f.Path); err == nil {
		_ = json.Unmarshal(payload, &values)
	}
	return values
}

func (f *FileSecrets) write(values map[string]string) error {
	payload, _ := json.Marshal(values)
	return os.WriteFile(f.Path, payload, 0o600)
}

func (f *FileSecrets) Get(key string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.read()[key], nil
}

func (f *FileSecrets) Set(key, value string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	values := f.read()
	values[key] = value
	return f.write(values)
}

func (f *FileSecrets) Delete(key string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	values := f.read()
	delete(values, key)
	return f.write(values)
}
