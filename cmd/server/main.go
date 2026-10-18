package main

import (
	"context"
	"errors"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/user/llm-crud1/internal/handlers"
	"github.com/user/llm-crud1/internal/store"
)

// envOr returns the environment value for key when set (an explicitly empty
// value counts as set, e.g. WEB_DIR= disables the GUI), else def.
func envOr(key, def string) string {
	if v, ok := os.LookupEnv(key); ok {
		return v
	}
	return def
}

func main() {
	// Flags win over env vars (ADDR, DB_PATH, WEB_DIR).
	addr := flag.String("addr", envOr("ADDR", ":8080"), "listen address")
	dbPath := flag.String("db", envOr("DB_PATH", "llm.db"), "SQLite database path (use :memory: to disable persistence)")
	webDir := flag.String("web", envOr("WEB_DIR", "web"), "static GUI directory (empty to disable)")
	flag.Parse()

	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	slog.SetDefault(log)

	var st store.Store
	if *dbPath == ":memory:" {
		st = store.NewMemoryStore()
		log.Info("store", "type", "memory")
	} else {
		sqlite, err := store.NewSQLiteStore(*dbPath)
		if err != nil {
			log.Error("open database", "path", *dbPath, "err", err)
			os.Exit(1)
		}
		defer sqlite.Close()
		st = sqlite
		log.Info("store", "type", "sqlite", "path", *dbPath)
	}

	mux := http.NewServeMux()
	api := handlers.API{Store: st, Log: log}
	api.Register(mux)

	if *webDir != "" {
		if info, err := os.Stat(*webDir); err == nil && info.IsDir() {
			mux.Handle("GET /", http.FileServer(http.Dir(*webDir)))
			log.Info("web gui enabled", "dir", *webDir)
		} else {
			log.Warn("web dir missing, GUI disabled", "dir", *webDir)
		}
	}

	srv := &http.Server{
		Addr:              *addr,
		Handler:           withMiddleware(log, mux),
		ReadHeaderTimeout: 5 * time.Second,
	}

	go func() {
		log.Info("server starting", "addr", *addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("server error", "err", err)
			os.Exit(1)
		}
	}()

	// Graceful shutdown on SIGINT/SIGTERM.
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop
	log.Info("shutting down")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		log.Error("shutdown", "err", err)
	}
}
