// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2025 Kirill aka Noiseonwires
package config

import "testing"

func TestHashAndVerifyWebUIPassword(t *testing.T) {
	hashed, err := HashWebUIPasswordForStorage("correct horse battery staple")
	if err != nil {
		t.Fatalf("HashWebUIPasswordForStorage: %v", err)
	}
	if !IsHashedWebUIPassword(hashed) {
		t.Fatalf("expected marked hash, got %q", hashed)
	}
	if hashed == "correct horse battery staple" {
		t.Fatal("password was not hashed")
	}
	if !VerifyWebUIPassword("correct horse battery staple", hashed) {
		t.Fatal("hashed password did not verify")
	}
	if VerifyWebUIPassword("wrong", hashed) {
		t.Fatal("wrong password verified")
	}
}

func TestHashWebUIPasswordInConfigValues(t *testing.T) {
	values := map[string]string{"web_ui.password": "plain"}
	changed, err := HashWebUIPasswordInConfigValues(values)
	if err != nil {
		t.Fatalf("HashWebUIPasswordInConfigValues: %v", err)
	}
	if !changed {
		t.Fatal("expected plaintext password to be hashed")
	}
	first := values["web_ui.password"]
	changed, err = HashWebUIPasswordInConfigValues(values)
	if err != nil {
		t.Fatalf("second HashWebUIPasswordInConfigValues: %v", err)
	}
	if changed {
		t.Fatal("already-hashed password was rehashed")
	}
	if values["web_ui.password"] != first {
		t.Fatal("already-hashed password changed")
	}
}

func TestConfigForExportHashesWebUIPassword(t *testing.T) {
	cfg := &Config{}
	cfg.WebUI.Password = "plain"

	exportCfg, err := ConfigForExport(cfg)
	if err != nil {
		t.Fatalf("ConfigForExport: %v", err)
	}
	if exportCfg == cfg {
		t.Fatal("ConfigForExport returned original config pointer")
	}
	if cfg.WebUI.Password != "plain" {
		t.Fatal("ConfigForExport mutated source config")
	}
	if !IsHashedWebUIPassword(exportCfg.WebUI.Password) {
		t.Fatalf("exported password was not hashed: %q", exportCfg.WebUI.Password)
	}
	if !VerifyWebUIPassword("plain", exportCfg.WebUI.Password) {
		t.Fatal("exported hash does not verify original password")
	}
}
