package rex

import (
	"errors"
	"strings"
	"testing"
)

type limitedSecrets struct {
	*MemorySecrets
	failSet    string
	failDelete string
}

func (s *limitedSecrets) Set(key, value string) error {
	if len(value) > 2560 {
		return errors.New("Windows credential too large")
	}
	if s.failSet != "" && (key == s.failSet || strings.HasSuffix(key, s.failSet)) {
		return errors.New("injected storage failure")
	}
	return s.MemorySecrets.Set(key, value)
}
func (s *limitedSecrets) Delete(key string) error {
	if s.failDelete != "" && strings.Contains(key, s.failDelete) {
		return errors.New("injected deletion failure")
	}
	return s.MemorySecrets.Delete(key)
}

func TestChunkedCredentialsRotateAndDelete(t *testing.T) {
	backend := &limitedSecrets{MemorySecrets: NewMemorySecrets()}
	store := chunkedSecrets{backend: backend}
	for _, value := range []string{"legacy", strings.Repeat("token-界", 2000), strings.Repeat("refreshed", 2000), "small again", credentialChunkPrefix + "literal"} {
		if err := store.Set("chatgpt", value); err != nil {
			t.Fatal(err)
		}
		got, err := store.Get("chatgpt")
		if err != nil || got != value {
			t.Fatal("credential round-trip failed", err)
		}
	}
	if err := store.Delete("chatgpt"); err != nil {
		t.Fatal(err)
	}
	if value, err := store.Get("chatgpt"); err != nil || value != "" {
		t.Fatal("credential survived deletion")
	}
	if len(backend.values) != 0 {
		t.Fatal("old credential parts leaked")
	}
}

func TestChunkedCredentialFailurePreservesPreviousGeneration(t *testing.T) {
	for _, failure := range []string{".1", "chatgpt"} {
		backend := &limitedSecrets{MemorySecrets: NewMemorySecrets()}
		store := chunkedSecrets{backend: backend}
		if err := store.Set("chatgpt", "working"); err != nil {
			t.Fatal(err)
		}
		backend.failSet = failure
		if err := store.Set("chatgpt", strings.Repeat("new token", 1000)); err == nil {
			t.Fatal("expected failed rotation")
		}
		backend.failSet = ""
		got, err := store.Get("chatgpt")
		if err != nil || got != "working" || len(backend.values) != 1 {
			t.Fatal("failed rotation damaged previous credential", err)
		}
	}
}

func TestChunkedCredentialDeletionCanRetry(t *testing.T) {
	backend := &limitedSecrets{MemorySecrets: NewMemorySecrets()}
	store := chunkedSecrets{backend: backend}
	if err := store.Set("chatgpt", strings.Repeat("token", 1000)); err != nil {
		t.Fatal(err)
	}
	backend.failDelete = ".part."
	if err := store.Delete("chatgpt"); err == nil {
		t.Fatal("expected cleanup failure")
	}
	if value, err := store.Get("chatgpt"); err != nil || value != "" {
		t.Fatal("deleted credential remained usable")
	}
	backend.failDelete = ""
	if err := store.Delete("chatgpt"); err != nil || len(backend.values) != 0 {
		t.Fatal("credential cleanup did not retry", err)
	}
}

func TestChunkedCredentialCorruptionFailsClosed(t *testing.T) {
	backend := NewMemorySecrets()
	store := chunkedSecrets{backend: backend}
	if err := store.Set("chatgpt", strings.Repeat("token", 1000)); err != nil {
		t.Fatal(err)
	}
	for key := range backend.values {
		if strings.Contains(key, ".part.") {
			backend.values[key] = "corrupted"
			break
		}
	}
	if _, err := store.Get("chatgpt"); err == nil {
		t.Fatal("accepted corrupted credential")
	}
}
