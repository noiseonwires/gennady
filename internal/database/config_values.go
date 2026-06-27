// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2025 Kirill aka Noiseonwires
package database

// Persistence of dynamic configuration as key/value pairs in SQLite.
//
// Mirrors the YAML config tree using flattened dotted keys. Used by:
//   - the web UI (admin can edit live config in DB)
//   - DB-as-config-source mode when no YAML file is present (see main.go)

// GetAllConfigValues returns all key-value pairs from the config_values table.
func (db *DB) GetAllConfigValues() (map[string]string, error) {
	rows, err := db.conn.Query(`SELECT key, value FROM config_values`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := make(map[string]string)
	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			return nil, err
		}
		result[key] = value
	}
	return result, rows.Err()
}

// SetAllConfigValues stores all key-value pairs in the config_values table,
// replacing any existing values. Keys not present in the new map are deleted
// to prevent stale indexed entries (e.g. removed models) from persisting.
func (db *DB) SetAllConfigValues(values map[string]string) error {
	return db.retryOnTransientError(func() error {
		tx, err := db.conn.Begin()
		if err != nil {
			return err
		}
		defer tx.Rollback()

		// Delete keys that are no longer in the config
		rows, err := tx.Query(`SELECT key FROM config_values`)
		if err != nil {
			return err
		}
		var staleKeys []string
		for rows.Next() {
			var k string
			if err := rows.Scan(&k); err != nil {
				rows.Close()
				return err
			}
			if _, exists := values[k]; !exists {
				staleKeys = append(staleKeys, k)
			}
		}
		rows.Close()

		if len(staleKeys) > 0 {
			delStmt, err := tx.Prepare(`DELETE FROM config_values WHERE key = ?`)
			if err != nil {
				return err
			}
			for _, k := range staleKeys {
				if _, err := delStmt.Exec(k); err != nil {
					delStmt.Close()
					return err
				}
			}
			delStmt.Close()
		}

		stmt, err := tx.Prepare(`INSERT INTO config_values (key, value, updated_at) VALUES (?, ?, CURRENT_TIMESTAMP)
			ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at`)
		if err != nil {
			return err
		}
		defer stmt.Close()

		for k, v := range values {
			if _, err := stmt.Exec(k, v); err != nil {
				return err
			}
		}
		return tx.Commit()
	}, "set all config values")
}

// GetConfigValue retrieves a single config value from the database.
func (db *DB) GetConfigValue(key string) (string, error) {
	var value string
	err := db.conn.QueryRow(`SELECT value FROM config_values WHERE key = ?`, key).Scan(&value)
	if err != nil {
		return "", err
	}
	return value, nil
}

// IsRemote returns true if the database is using the remote provider.
func (db *DB) IsRemote() bool {
	return db.provider == ProviderRemote
}

// SetConfigValue stores a single config key-value pair in the database.
func (db *DB) SetConfigValue(key, value string) error {
	return db.retryOnTransientError(func() error {
		_, err := db.conn.Exec(`INSERT INTO config_values (key, value, updated_at) VALUES (?, ?, CURRENT_TIMESTAMP)
			ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at`, key, value)
		return err
	}, "set config value")
}
