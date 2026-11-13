// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2025 Kirill aka Noiseonwires
package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.yaml.in/yaml/v3"
)

func TestValidateBackupEncryption(t *testing.T) {
	c := &Config{Backup: BackupConfig{Enabled: true, Target: "local", LocalPath: "./backups"}}
	require.NoError(t, c.validateBackup(), "encryption remains opt-in")
	c.Backup.Encrypt = true
	require.ErrorContains(t, c.validateBackup(), "backup.encryption_password")
	c.Backup.EncryptionPassword = " \t"
	require.Error(t, c.validateBackup())
	c.Backup.EncryptionPassword = "  keep surrounding spaces  "
	require.NoError(t, c.validateBackup())
	c.Backup.Enabled = false
	c.Backup.EncryptionPassword = ""
	require.NoError(t, c.validateBackup(), "disabled scheduling does not require a complete destination")
	require.Error(t, c.Backup.ValidateEncryption(), "manual runs must still validate encryption")
}

func TestBackupEncryptionConfigRoundTrip(t *testing.T) {
	var cfg Config
	require.NoError(t, yaml.Unmarshal([]byte("backup:\n  encrypt: true\n  encryption_password: '  secret  '\n"), &cfg))
	assert.True(t, cfg.Backup.Encrypt)
	assert.Equal(t, "  secret  ", cfg.Backup.EncryptionPassword)

	var restored Config
	ApplyStringMap(&restored, ConfigToStringMap(&cfg))
	assert.Equal(t, cfg.Backup, restored.Backup)
	assert.True(t, SensitiveConfigKeys()["backup.encryption_password"])

	t.Setenv("BACKUP_ENCRYPT", "false")
	t.Setenv("BACKUP_ENCRYPTION_PASSWORD", "  environment secret  ")
	applyEnvOverrides(&cfg)
	assert.False(t, cfg.Backup.Encrypt)
	assert.Equal(t, "  environment secret  ", cfg.Backup.EncryptionPassword)
}
