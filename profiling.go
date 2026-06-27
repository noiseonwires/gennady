// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2025 Kirill aka Noiseonwires
package main

import (
	"encoding/json"
	"log"
	"net/http"
	"net/http/pprof"
	"runtime"
	"strings"
	"time"
)

// startProfilingServer optionally starts a localhost-only diagnostics HTTP
// server exposing Go's pprof profiles and a /debug/memstats snapshot. It is a
// no-op unless the PPROF_ADDR environment variable is set, so production runs
// are unaffected.
//
// Usage:
//
//	PPROF_ADDR=1            → listen on 127.0.0.1:6060 (convenience shortcut)
//	PPROF_ADDR=:6061        → listen on :6061 (all interfaces - warns)
//	PPROF_ADDR=127.0.0.1:7000
//
// Then capture profiles with, e.g.:
//
//	go tool pprof http://127.0.0.1:6060/debug/pprof/heap        # live heap
//	go tool pprof http://127.0.0.1:6060/debug/pprof/allocs      # all allocations
//	curl http://127.0.0.1:6060/debug/pprof/goroutine?debug=1    # goroutine dump
//	curl http://127.0.0.1:6060/debug/memstats                   # quick mem snapshot
//
// pprof endpoints expose internal state, so the listener defaults to loopback;
// binding it to a public address logs a warning.
func startProfilingServer(addr string) {
	addr = strings.TrimSpace(addr)
	if addr == "" {
		return
	}
	switch strings.ToLower(addr) {
	case "1", "true", "yes", "on":
		addr = "127.0.0.1:6060"
	}

	if !strings.HasPrefix(addr, "127.0.0.1:") && !strings.HasPrefix(addr, "localhost:") {
		log.Printf("⚠️  PPROF_ADDR=%q is not loopback-only - pprof exposes internal state; bind it to 127.0.0.1 or front it with auth.", addr)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/debug/pprof/", pprof.Index)
	mux.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
	mux.HandleFunc("/debug/pprof/profile", pprof.Profile)
	mux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
	mux.HandleFunc("/debug/pprof/trace", pprof.Trace)
	mux.HandleFunc("/debug/memstats", func(w http.ResponseWriter, _ *http.Request) {
		var m runtime.MemStats
		runtime.ReadMemStats(&m)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"alloc_bytes":         m.Alloc,     // live heap in use
			"sys_bytes":           m.Sys,       // total obtained from OS (≈ RSS ceiling)
			"heap_alloc_bytes":    m.HeapAlloc, // live heap objects
			"heap_sys_bytes":      m.HeapSys,   // heap memory obtained from OS
			"heap_idle_bytes":     m.HeapIdle,  // idle (returnable) heap spans
			"heap_released_bytes": m.HeapReleased,
			"heap_objects":        m.HeapObjects,
			"stack_sys_bytes":     m.StackSys, // goroutine stacks
			"num_goroutine":       runtime.NumGoroutine(),
			"num_gc":              m.NumGC,
			"gc_cpu_fraction":     m.GCCPUFraction,
			"next_gc_bytes":       m.NextGC,
		})
	})

	srv := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() {
		log.Printf("🔬 Profiling server listening on http://%s/debug/pprof/ (set via PPROF_ADDR)", addr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Printf("Profiling server error: %v", err)
		}
	}()
}
