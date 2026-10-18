// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

// Command printshop-mcp serves Bambuddy and Spoolman to MCP clients. It speaks
// stdio by default, which is what `claude mcp add` wants, and streamable HTTP
// when PRINTSHOP_MCP_LISTEN is set. Both services are reached over their REST
// APIs; nothing here touches a database.
package main

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/fireball1725/printshop-mcp/internal/api"
	"github.com/fireball1725/printshop-mcp/internal/config"
	"github.com/fireball1725/printshop-mcp/internal/hms"
	"github.com/fireball1725/printshop-mcp/internal/tools"
	"github.com/fireball1725/printshop-mcp/internal/version"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func main() {
	// Logs go to stderr: on stdio, stdout is the protocol.
	logger := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	slog.SetDefault(logger)

	cfg, err := config.Load()
	if err != nil {
		logger.Error("startup config invalid", "error", err)
		os.Exit(1)
	}
	logger.Info("printshop-mcp", "version", version.String(), "bambuddy", cfg.BambuddyURL, "spoolman", cfg.SpoolmanURL, "http", cfg.Listen != "")

	var bbHeaders map[string]string
	if cfg.BambuddyAPIKey != "" {
		bbHeaders = map[string]string{"X-API-Key": cfg.BambuddyAPIKey}
	}
	hmsBase := ""
	if cfg.HMSLookup {
		hmsBase = hms.DefaultBase
	}
	deps := &tools.Deps{
		Bambuddy:    api.New("bambuddy", cfg.BambuddyURL, bbHeaders),
		Spoolman:    api.New("spoolman", cfg.SpoolmanURL, nil),
		Model:       cfg.PrinterModel,
		HMS:         hms.New(hmsBase),
		BambuddyURL: cfg.BambuddyURL,
	}

	srv := mcp.NewServer(&mcp.Implementation{Name: "printshop", Version: version.String()}, nil)
	tools.RegisterAll(srv, deps)

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	if cfg.Listen == "" {
		if err := srv.Run(ctx, &mcp.StdioTransport{}); err != nil && ctx.Err() == nil {
			logger.Error("stdio server stopped", "error", err)
			os.Exit(1)
		}
		return
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok", "version": version.String()})
	})
	mux.Handle("/mcp", requireBearer(cfg.MCPToken, mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return srv }, nil)))
	hs := &http.Server{Addr: cfg.Listen, Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	go func() {
		<-ctx.Done()
		sctx, c := context.WithTimeout(context.Background(), 5*time.Second)
		defer c()
		_ = hs.Shutdown(sctx)
	}()
	logger.Info("listening", "addr", cfg.Listen)
	if err := hs.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		logger.Error("http server crashed", "error", err)
		os.Exit(1)
	}
}

// requireBearer gates MCP traffic with a constant-time token compare.
func requireBearer(token string, next http.Handler) http.Handler {
	want := []byte(token)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got := []byte(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
		if len(got) == 0 || subtle.ConstantTimeCompare(got, want) != 1 {
			http.Error(w, `{"error":"missing or invalid authorization header"}`, http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}
