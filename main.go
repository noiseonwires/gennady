// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2025 Kirill aka Noiseonwires
package main

import (
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"gennadium/internal/bot"
	"gennadium/internal/config"
	"gennadium/internal/database"
	"gennadium/internal/i18n"
	"gennadium/internal/web"
)

func hashDBWebUIPasswordAndReload(db *database.DB, values map[string]string) (map[string]string, bool, error) {
	changed, err := config.HashWebUIPasswordInConfigValues(values)
	if err != nil || !changed {
		return values, changed, err
	}
	if err := db.SetConfigValue("web_ui.password", values["web_ui.password"]); err != nil {
		return values, false, err
	}
	reloaded, err := db.GetAllConfigValues()
	if err != nil {
		return values, false, err
	}
	return reloaded, true, nil
}

// Build information (set via -ldflags during build)
var (
	version   = "dev"
	gitCommit = "unknown"
	buildTime = "unknown"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout))
}

// printBanner writes the startup banner (version, copyright, environment) to w.
func printBanner(w io.Writer) {
	fmt.Fprintf(w, "%s Telegram Bot 🤖\n", bot.BotName)
	fmt.Fprintf(w, "* Version:    %s\n", version)
	fmt.Fprintf(w, "* Git Commit: %s\n", gitCommit)
	fmt.Fprintf(w, "* Build Time: %s\n", buildTime)
	fmt.Fprintf(w, "* %s\n", bot.CopyrightNotice(buildTime))
	fmt.Fprintf(w, "* URL:        %s\n", bot.BotURL)
	fmt.Fprintf(w, "* This software is licensed under the GNU Affero General Public License v3 (AGPL-3) or a commercial license. See LICENSE file.\n")

	// Print BunnyNet environment variables if set
	for _, envVar := range []string{"BUNNYNET_MC_APPID", "BUNNYNET_MC_PODID", "BUNNYNET_MC_REGION"} {
		if v := os.Getenv(envVar); v != "" {
			fmt.Fprintf(w, "* %s: %s\n", envVar, v)
		}
	}
}

// run parses command-line flags and dispatches to the appropriate action,
// returning the process exit code. Early-exit flags (version, doc generation,
// env export) are handled inline so they can be unit-tested; the long-running
// bot is delegated to runBot.
func run(args []string, stdout io.Writer) int {
	fs := flag.NewFlagSet("gennadium", flag.ContinueOnError)
	fs.SetOutput(stdout)
	var (
		showVersion   = fs.Bool("version", false, "Show version information")
		configFile    = fs.String("config", "config.yaml", "Path to configuration file (YAML)")
		exportEnv     = fs.Bool("export-env", false, "Export effective configuration as environment variables and exit")
		genConfigDocs = fs.String("generate-config-docs", "", "Generate config reference markdown file and exit (specify output path)")
	)
	if err := fs.Parse(args); err != nil {
		return 2
	}

	printBanner(stdout)

	// Handle version flag
	if *showVersion {
		return 0
	}

	// Handle generate-config-docs flag (no config file needed)
	if *genConfigDocs != "" {
		dataDir := "internal/web/data"
		if err := config.GenerateConfigDocs(dataDir, *genConfigDocs); err != nil {
			fmt.Fprintf(stdout, "Failed to generate config docs: %v\n", err)
			return 1
		}
		ext := filepath.Ext(*genConfigDocs)
		base := strings.TrimSuffix(*genConfigDocs, ext)
		fmt.Fprintf(stdout, "Config reference generated: %s_*%s\n", base, ext)
		return 0
	}

	// Handle export-env flag
	if *exportEnv {
		cfg, err := config.Load(*configFile)
		if err != nil {
			fmt.Fprintf(stdout, "Failed to load config: %v\n", err)
			return 1
		}
		fmt.Fprint(stdout, config.ExportEnvVars(cfg))
		return 0
	}

	return runBot(*configFile)
}

// runBot loads configuration, initializes the database and runs the bot until
// shutdown, supporting in-process soft restarts. It returns the process exit
// code. This is the production path and relies on log.Fatalf for fatal startup
// errors, so it is exercised by integration/manual runs rather than unit tests.
func runBot(configFile string) int {
	// Load configuration
	configFileExists := true
	if _, statErr := os.Stat(configFile); os.IsNotExist(statErr) {
		configFileExists = false
	}

	cfg, err := config.Load(configFile)
	if err != nil {
		log.Fatalf("❌ Configuration is invalid; the bot cannot start. Fix every issue below:\n%v", err)
	}

	// Initialize database
	dbCfg := database.Config{
		Provider:  cfg.Database.Provider,
		Path:      cfg.Database.Path,
		URL:       cfg.Database.URL,
		AuthToken: cfg.Database.AuthToken,
	}

	// If no config file is present, load configuration from the database. This
	// applies to both remote and local providers: the connection details come
	// from env vars / defaults via config.Load above, and the DB's config_values
	// table is the source of truth in this mode (matching configFromDB below).
	var db *database.DB

	if !configFileExists {
		earlyDB, dbErr := database.Init(dbCfg)
		if dbErr != nil {
			log.Fatalf("No config file found and failed to connect to database: %v", dbErr)
		}

		kv, kvErr := earlyDB.GetAllConfigValues()
		if kvErr != nil {
			earlyDB.Close()
			log.Fatalf("Failed to read config from database: %v", kvErr)
		}

		if len(kv) == 0 {
			// DB is empty - seed from config.example.yaml + env overrides
			log.Println("📦 Database config is empty, seeding from config.example.yaml (with env overrides)...")
			exampleCfg, exErr := config.Load("config.example.yaml")
			if exErr != nil {
				earlyDB.Close()
				log.Fatalf("Failed to load config.example.yaml for DB seeding: %v", exErr)
			}
			kv, kvErr = config.ConfigToDBStringMap(exampleCfg)
			if kvErr != nil {
				earlyDB.Close()
				log.Fatalf("Failed to prepare config for database: %v", kvErr)
			}
			if err := earlyDB.SetAllConfigValues(kv); err != nil {
				earlyDB.Close()
				log.Fatalf("Failed to save config to database: %v", err)
			}
			log.Printf("✓ Seeded %d config values into database", len(kv))
			kv, kvErr = earlyDB.GetAllConfigValues()
			if kvErr != nil {
				earlyDB.Close()
				log.Fatalf("Failed to re-read seeded config from database: %v", kvErr)
			}
		} else if reloadedKV, changed, hashErr := hashDBWebUIPasswordAndReload(earlyDB, kv); hashErr != nil {
			earlyDB.Close()
			log.Fatalf("Failed to hash web UI password from database: %v", hashErr)
		} else if changed {
			kv = reloadedKV
			log.Println("✓ Migrated web UI password in database to hashed format")
		}

		// Build config from DB values (env vars override, defaults fill gaps)
		cfg, err = config.LoadFromStringMap(kv)
		if err != nil {
			earlyDB.Close()
			log.Fatalf("❌ Configuration built from the database is invalid; the bot cannot start. Fix every issue below (note that environment variables override stored values):\n%v", err)
		}
		log.Println("✓ Configuration loaded from database")

		// Reuse DB connection - re-read dbCfg in case cfg changed
		db = earlyDB
		dbCfg = database.Config{
			Provider:  cfg.Database.Provider,
			Path:      cfg.Database.Path,
			URL:       cfg.Database.URL,
			AuthToken: cfg.Database.AuthToken,
		}
	}

	// Check for missing required config values.
	// When the web UI is enabled with usable authentication, allow the bot to start
	// without admin/moderation chat IDs or bot_token so the user can configure them
	// through the web interface. "Usable auth" means a password is set, OR OTP is
	// enabled with both a super-admin user id and a bot token to deliver codes.
	if missing := cfg.MissingConfigFields(); len(missing) > 0 {
		if cfg.WebUI.Enabled && cfg.HasUsableWebUIAuth() {
			log.Printf("⚠️  Missing required configuration values: %s. Bot features depending on these will be disabled until configured via the web UI.", strings.Join(missing, ", "))
		} else {
			if db != nil {
				db.Close()
			}
			if cfg.WebUI.Enabled {
				log.Fatalf("❌ Missing required configuration values: %s. The web UI is enabled but has no usable authentication (set web_ui.password, or enable OTP with admin.super_admin_user_id and a valid bot_token). Refusing to start.", strings.Join(missing, ", "))
			}
			log.Fatalf("❌ Missing required configuration values: %s. Please check your config file, set required environment variables, or enable the web UI (with authentication) to configure them interactively.", strings.Join(missing, ", "))
		}
	}

	// Initialize bot localization
	lang := cfg.Language
	if lang == "" {
		lang = "ru"
	}
	i18n.Init(lang)

	// Initialize database (if not already connected via DB config path)
	if db == nil {
		db, err = database.Init(dbCfg)
		if err != nil {
			log.Fatalf("Failed to initialize database: %v", err)
		}
	}
	defer db.Close()

	// Log config source
	if configFileExists {
		log.Printf("📄 Config source: file (%s)", configFile)
	} else if database.ResolveProvider(dbCfg.Provider, dbCfg.URL, dbCfg.AuthToken) == database.ProviderRemote {
		log.Printf("🗄️ Config source: database (remote: %s)", dbCfg.URL)
	} else {
		log.Printf("🗄️ Config source: database (local: %s)", dbCfg.Path)
	}

	// Clean up any leftover temp export files from previous runs
	database.CleanupTempExports(filepath.Dir(cfg.Database.Path))

	// Persistent objects that survive soft restarts
	configFromDB := !configFileExists
	restartCh := make(chan struct{}, 1)
	shutdownCh := make(chan os.Signal, 1)
	signal.Notify(shutdownCh, os.Interrupt, syscall.SIGTERM)

	var logBuffer *web.LogBuffer
	var diagnostics *web.DiagnosticsTracker

	// Optional localhost-only pprof/memstats server for diagnosing memory and
	// goroutine usage. No-op unless PPROF_ADDR is set.
	startProfilingServer(os.Getenv("PPROF_ADDR"))

	firstRun := true

	for {
		// Reload config on soft restart
		if !firstRun {
			log.Println("🔄 Reloading configuration...")
			var reloadErr error
			if configFromDB {
				kv, kvErr := db.GetAllConfigValues()
				if kvErr != nil {
					log.Printf("❌ Failed to read config from database: %v, shutting down", kvErr)
					break
				}
				if reloadedKV, changed, hashErr := hashDBWebUIPasswordAndReload(db, kv); hashErr != nil {
					log.Printf("❌ Failed to hash web UI password from database: %v, shutting down", hashErr)
					break
				} else if changed {
					kv = reloadedKV
					log.Println("✓ Migrated web UI password in database to hashed format")
				}
				cfg, reloadErr = config.LoadFromStringMap(kv)
			} else {
				cfg, reloadErr = config.Load(configFile)
			}
			if reloadErr != nil {
				log.Printf("❌ Failed to reload config: %v, shutting down", reloadErr)
				break
			}
			i18n.Init(cfg.Language)
			log.Println("✓ Configuration reloaded")

			// Re-validate required fields after reload.
			// If web UI is disabled (or has no usable auth) and required values are still
			// missing, we cannot keep the bot in a degraded state with no way to fix it.
			if missing := cfg.MissingConfigFields(); len(missing) > 0 {
				if cfg.WebUI.Enabled && cfg.HasUsableWebUIAuth() {
					log.Printf("⚠️  Missing required configuration values after reload: %s. Bot features depending on these will be disabled until configured via the web UI.", strings.Join(missing, ", "))
				} else {
					log.Printf("❌ Missing required configuration values after reload: %s, and the web UI is disabled or has no usable authentication. Shutting down.", strings.Join(missing, ", "))
					break
				}
			}
		}
		firstRun = false

		// Initialize bot
		telegramBot, err := bot.New(cfg, db)
		if err != nil {
			log.Fatalf("Failed to initialize bot: %v", err)
		}

		telegramBot.SetBuildInfo(version, gitCommit, buildTime)

		// Set up web UI if enabled
		if cfg.WebUI.Enabled {
			// Refuse to expose an unauthenticated admin UI on a remotely
			// reachable address. Operators who intentionally front it with their
			// own auth proxy can override with WEB_UI_ALLOW_NO_AUTH=1.
			if !cfg.HasUsableWebUIAuth() && !cfg.ServerBindIsLoopbackOnly() && os.Getenv("WEB_UI_ALLOW_NO_AUTH") != "1" {
				log.Fatalf("❌ Web UI is enabled with no authentication on a non-loopback address %q. Refusing to start to avoid exposing admin endpoints (config read/write, DB upload/download, restart). Set web_ui.password, or super_admin_user_id + bot_token for OTP, bind server.listen_addr to 127.0.0.1, or set WEB_UI_ALLOW_NO_AUTH=1 to override.", cfg.Server.ListenAddr)
			}
			if logBuffer == nil {
				logBuffer = web.NewLogBuffer(1000)
				log.SetOutput(io.MultiWriter(os.Stderr, logBuffer))
			}
			if diagnostics == nil {
				diagnostics = web.NewDiagnosticsTracker()
			}

			webServer := web.NewWebServer(cfg, db, diagnostics, configFile, configFromDB)
			webServer.SetLogBuffer(logBuffer)

			telegramBot.SetDiagnostics(diagnostics)
			telegramBot.SetTelegramStatusReporter(diagnostics)
			telegramBot.SetWebOTPGenerator(func() string {
				return webServer.Auth().GenerateOTP()
			})
			webServer.SetSendOTP(func(code string) error {
				return telegramBot.SendOTPToSuperAdmin(code)
			})
			telegramBot.SetHTTPMux(webServer.Mux)
			webServer.SetAPITester(telegramBot)
			webServer.SetChatNameResolver(telegramBot)
			webServer.SetChatLister(telegramBot)
			webServer.SetTopicNameResolver(telegramBot)
			webServer.SetModerator(telegramBot)
			webServer.SetScheduledEventsTrigger(telegramBot)
			webServer.SetBuildInfo(version, gitCommit, buildTime, bot.BotURL)
			webServer.SetRestartFunc(func(mode string) {
				switch mode {
				case "hard":
					log.Println("Hard restart triggered from Web UI, exiting process...")
					telegramBot.Stop()
					os.Exit(0)
				default:
					log.Println("Soft restart triggered from Web UI...")
					select {
					case restartCh <- struct{}{}:
					default:
					}
				}
			})

			hasPassword := cfg.WebUI.Password != ""
			hasOTP := cfg.WebUI.IsOTPEnabled() && cfg.Admin.SuperAdminUserID != 0

			switch {
			case hasPassword && hasOTP:
				log.Println("🔐 Web UI: password + OTP (sent to super-admin via Telegram)")
			case hasPassword:
				log.Println("🔐 Web UI: password-only authentication")
			case hasOTP:
				log.Println("🔐 Web UI: OTP-only authentication (sent to super-admin via Telegram)")
			default:
				log.Println("⚠️  Web UI is enabled but no authentication is configured (set web_ui.password or super_admin_user_id)")
			}
		}

		go telegramBot.Start()

		// Wait for shutdown or soft restart signal
		select {
		case <-shutdownCh:
			log.Println("Shutting down bot...")
			telegramBot.Stop()
			return 0
		case <-restartCh:
			log.Println("🔄 Soft restart: stopping bot...")
			telegramBot.Stop()
			log.Println("🔄 Soft restart: bot stopped, reloading...")
			continue
		}
	}

	// The loop only exits via `break` on an unrecoverable reload error after a
	// soft restart; treat that as a clean shutdown.
	return 0
}
