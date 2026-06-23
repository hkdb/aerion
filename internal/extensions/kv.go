package extensions

import (
	"database/sql"
	"fmt"
	"strings"

	coreapi "github.com/hkdb/aerion/internal/core/api/v1"
)

// KV returns the v1.KVStore backed by this Store's ext_kv table.
func (s *Store) KV() coreapi.KVStore {
	return &kvStore{db: s.db.DB}
}

type kvStore struct {
	db *sql.DB
}

// Get returns the value for key, or an empty string + nil error if not found.
// This mirrors the existing settings/store.go convention used in core.
func (k *kvStore) Get(key string) (string, error) {
	var v string
	err := k.db.QueryRow("SELECT value FROM ext_kv WHERE key = ?", key).Scan(&v)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("kv get %q: %w", key, err)
	}
	return v, nil
}

func (k *kvStore) Set(key, value string) error {
	_, err := k.db.Exec(`
		INSERT INTO ext_kv (key, value, updated_at)
		VALUES (?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = CURRENT_TIMESTAMP
	`, key, value)
	if err != nil {
		return fmt.Errorf("kv set %q: %w", key, err)
	}
	return nil
}

func (k *kvStore) Delete(key string) error {
	_, err := k.db.Exec("DELETE FROM ext_kv WHERE key = ?", key)
	if err != nil {
		return fmt.Errorf("kv delete %q: %w", key, err)
	}
	return nil
}

// List returns all keys with the given prefix, in ascending key order.
// Pass "" to list all keys.
func (k *kvStore) List(prefix string) ([]string, error) {
	if prefix == "" {
		return k.queryKeys("SELECT key FROM ext_kv ORDER BY key ASC")
	}
	// Escape LIKE wildcards (% and _) so a prefix containing them matches literally.
	esc := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(prefix)
	return k.queryKeys(`SELECT key FROM ext_kv WHERE key LIKE ? ESCAPE '\' ORDER BY key ASC`, esc+"%")
}

func (k *kvStore) queryKeys(q string, args ...interface{}) ([]string, error) {
	rows, err := k.db.Query(q, args...)
	if err != nil {
		return nil, fmt.Errorf("kv list: %w", err)
	}
	defer rows.Close()

	var keys []string
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			return nil, fmt.Errorf("kv list scan: %w", err)
		}
		keys = append(keys, key)
	}
	return keys, rows.Err()
}
