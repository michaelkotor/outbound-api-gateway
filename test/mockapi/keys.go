package main

import (
	"fmt"
	"strings"
)

// KeyRecord is a single registered key in the mock API.
type KeyRecord struct {
	Name        string
	Token       string // raw, only used for lookup — never returned in responses
	Fingerprint string // "***" + last 3 chars of Token
}

// Registry holds the configured keys indexed for O(1) auth and admin lookups.
type Registry struct {
	byToken map[string]*KeyRecord
	byName  map[string]*KeyRecord
	names   []string // registration order is preserved for stable logging
}

// parseRegistry parses a MOCK_KEYS value ("name:token,name:token,...") into a
// Registry. It fails fast on duplicate names or duplicate tokens. Token values
// are never included in error messages.
func parseRegistry(raw string) (*Registry, error) {
	r := &Registry{
		byToken: make(map[string]*KeyRecord),
		byName:  make(map[string]*KeyRecord),
	}

	pairs := strings.Split(raw, ",")
	for i, pair := range pairs {
		pair = strings.TrimSpace(pair)
		if pair == "" {
			return nil, fmt.Errorf("MOCK_KEYS entry %d is empty", i+1)
		}
		name, token, ok := strings.Cut(pair, ":")
		name = strings.TrimSpace(name)
		token = strings.TrimSpace(token)
		if !ok || name == "" || token == "" {
			return nil, fmt.Errorf("MOCK_KEYS entry %d must be in name:token form", i+1)
		}

		if _, exists := r.byName[name]; exists {
			return nil, fmt.Errorf("MOCK_KEYS: duplicate key name %q", name)
		}
		if _, exists := r.byToken[token]; exists {
			return nil, fmt.Errorf("MOCK_KEYS: duplicate token for key %q (tokens must be unique)", name)
		}

		rec := &KeyRecord{
			Name:        name,
			Token:       token,
			Fingerprint: fingerprint(token),
		}
		r.byToken[token] = rec
		r.byName[name] = rec
		r.names = append(r.names, name)
	}

	if len(r.names) == 0 {
		return nil, fmt.Errorf("MOCK_KEYS produced no keys")
	}
	return r, nil
}

// lookupByToken returns the key matching a raw token, or nil.
func (r *Registry) lookupByToken(token string) *KeyRecord {
	return r.byToken[token]
}

// fingerprint returns "***" + the last 3 chars of token. Shorter tokens use
// whatever characters are available.
func fingerprint(token string) string {
	last := token
	if len(token) > 3 {
		last = token[len(token)-3:]
	}
	return "***" + last
}
