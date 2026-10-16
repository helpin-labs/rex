package rex

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"sync"
)

const credentialChunkPrefix = "rex-credential-parts-v1:"
const credentialChunkBytes = 2000
const maxCredentialBytes = 1024 * 1024

var credentialPartsMu sync.Mutex

type credentialParts struct {
	Generation string `json:"generation"`
	Count      int    `json:"count"`
	SHA256     string `json:"sha256"`
	Deleted    bool   `json:"deleted,omitempty"`
}

type chunkedSecrets struct{ backend Secrets }

func parseCredentialParts(raw string) (*credentialParts, error) {
	if !strings.HasPrefix(raw, credentialChunkPrefix) {
		return nil, nil
	}
	var parts credentialParts
	if json.Unmarshal([]byte(strings.TrimPrefix(raw, credentialChunkPrefix)), &parts) != nil || len(parts.Generation) != 32 || len(parts.SHA256) != 64 || parts.Count < 1 || parts.Count > (maxCredentialBytes+credentialChunkBytes-1)/credentialChunkBytes {
		return nil, errors.New("saved credential manifest is invalid")
	}
	if _, err := hex.DecodeString(parts.Generation); err != nil {
		return nil, errors.New("saved credential generation is invalid")
	}
	if _, err := hex.DecodeString(parts.SHA256); err != nil {
		return nil, errors.New("saved credential checksum is invalid")
	}
	return &parts, nil
}

func credentialPartKey(key string, parts *credentialParts, index int) string {
	return key + ".part." + parts.Generation + "." + strconv.Itoa(index)
}

func (s chunkedSecrets) Get(key string) (string, error) {
	credentialPartsMu.Lock()
	defer credentialPartsMu.Unlock()
	raw, err := s.backend.Get(key)
	if err != nil {
		return "", err
	}
	parts, err := parseCredentialParts(raw)
	if err != nil {
		return "", err
	}
	if parts == nil {
		return raw, nil
	}
	if parts.Deleted {
		return "", nil
	}
	var value strings.Builder
	for i := 0; i < parts.Count; i++ {
		chunk, err := s.backend.Get(credentialPartKey(key, parts, i))
		if err != nil {
			return "", err
		}
		if chunk == "" || len(chunk) > credentialChunkBytes {
			return "", errors.New("saved credential is incomplete")
		}
		value.WriteString(chunk)
	}
	hash := sha256.Sum256([]byte(value.String()))
	if hex.EncodeToString(hash[:]) != parts.SHA256 {
		return "", errors.New("saved credential checksum does not match")
	}
	return value.String(), nil
}

func (s chunkedSecrets) removeParts(key string, parts *credentialParts) error {
	if parts == nil {
		return nil
	}
	var first error
	for i := 0; i < parts.Count; i++ {
		if err := s.backend.Delete(credentialPartKey(key, parts, i)); err != nil && first == nil {
			first = err
		}
	}
	return first
}

func (s chunkedSecrets) Set(key, value string) error {
	credentialPartsMu.Lock()
	defer credentialPartsMu.Unlock()
	if len(value) > maxCredentialBytes {
		return errors.New("credential exceeds the storage limit")
	}
	previous, err := s.backend.Get(key)
	if err != nil {
		return err
	}
	old, _ := parseCredentialParts(previous)
	var parts *credentialParts
	raw := value
	if len(value) > credentialChunkBytes || strings.HasPrefix(value, credentialChunkPrefix) {
		var generation [16]byte
		if _, err := rand.Read(generation[:]); err != nil {
			return err
		}
		hash := sha256.Sum256([]byte(value))
		parts = &credentialParts{Generation: hex.EncodeToString(generation[:]), Count: (len(value) + credentialChunkBytes - 1) / credentialChunkBytes, SHA256: hex.EncodeToString(hash[:])}
		for i := 0; i < parts.Count; i++ {
			chunk := value[i*credentialChunkBytes : min((i+1)*credentialChunkBytes, len(value))]
			if err := s.backend.Set(credentialPartKey(key, parts, i), chunk); err != nil {
				s.removeParts(key, parts)
				return err
			}
		}
		data, _ := json.Marshal(parts)
		raw = credentialChunkPrefix + string(data)
	}
	// Publish a single manifest only after every part exists. Readers see one generation.
	if err := s.backend.Set(key, raw); err != nil {
		s.removeParts(key, parts)
		return err
	}
	if err := s.removeParts(key, old); err != nil {
		slog.Warn("could not remove replaced credential parts", "error", err)
	}
	return nil
}

func (s chunkedSecrets) Delete(key string) error {
	credentialPartsMu.Lock()
	defer credentialPartsMu.Unlock()
	raw, err := s.backend.Get(key)
	if err != nil {
		return err
	}
	parts, err := parseCredentialParts(raw)
	if err != nil {
		return err
	}
	if parts != nil {
		// Keep a tombstone until cleanup succeeds, so deletion can be retried safely.
		parts.Deleted = true
		data, _ := json.Marshal(parts)
		if err := s.backend.Set(key, credentialChunkPrefix+string(data)); err != nil {
			return err
		}
		if err := s.removeParts(key, parts); err != nil {
			return fmt.Errorf("delete credential parts: %w", err)
		}
	}
	return s.backend.Delete(key)
}
