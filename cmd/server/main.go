package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/GssMahadevan/gvn-explorer/frontend"
	"github.com/GssMahadevan/gvn-explorer/internal/acl"
	"github.com/GssMahadevan/gvn-explorer/internal/audit"
	"github.com/GssMahadevan/gvn-explorer/internal/drafts"
	"github.com/GssMahadevan/gvn-explorer/internal/auth"
	"github.com/GssMahadevan/gvn-explorer/internal/db"
	"github.com/GssMahadevan/gvn-explorer/internal/fsx"
	"github.com/GssMahadevan/gvn-explorer/internal/handlers"
)

// envOr returns the environment value for key when set (an explicitly empty
// value counts as set), else def.
func envOr(key, def string) string {
	if v, ok := os.LookupEnv(key); ok {
		return v
	}
	return def
}

// envIntOr / envFloatOr are the numeric variants: an env value that fails to
// parse is a hard startup error, never a silent fall back to the default —
// a typo in compose config must not hide a limit the operator set.
func envIntOr(key string, def int) int {
	if v, ok := os.LookupEnv(key); ok {
		n, err := strconv.Atoi(v)
		if err != nil {
			fmt.Fprintf(os.Stderr, "%s: invalid integer %q\n", key, v)
			os.Exit(2)
		}
		return n
	}
	return def
}

func envFloatOr(key string, def float64) float64 {
	if v, ok := os.LookupEnv(key); ok {
		f, err := strconv.ParseFloat(v, 64)
		if err != nil {
			fmt.Fprintf(os.Stderr, "%s: invalid number %q\n", key, v)
			os.Exit(2)
		}
		return f
	}
	return def
}

func main() {
	// Flags win over env vars; every knob is env-settable so the Docker
	// image needs nothing but -e (see deployments/docker/README.md).
	addr := flag.String("addr", envOr("ADDR", ":8080"), "listen address")
	root := flag.String("root", envOr("ROOT", "data"), "root directory exposed to the file explorer")
	dbPath := flag.String("db", envOr("DB_PATH", "perms.db"), "SQLite database for users/groups/ACLs/sessions")
	adminFlag := flag.String("admin", envOr("ADMIN", "system:admin:admin"), "bootstrap super admin as tenant:user:pass (used only when no users exist)")
	webDir := flag.String("web", envOr("WEB_DIR", "web"), "static GUI directory (empty to disable)")
	maxMB := flag.Int("max-mb", envIntOr("MAX_MB", 10), "max file size for read/write/edit in MB")
	rps := flag.Float64("rps", envFloatOr("RPS", 20), "requests/sec per principal on API endpoints (0 disables rate limiting)")
	rburst := flag.Int("rburst", envIntOr("R_BURST", 60), "burst allowance for -rps")
	authRPS := flag.Float64("auth-rps", envFloatOr("AUTH_RPS", 3), "login requests/sec per source IP (0 disables)")
	authBurst := flag.Int("auth-burst", envIntOr("AUTH_BURST", 10), "burst allowance for -auth-rps")
	logLevel := flag.String("log-level", envOr("LOG_LEVEL", "info"), "log level: debug|info|warn|error")
	logJSON := flag.Bool("log-json", envOr("LOG_JSON", "true") == "true", "emit JSON logs (false = human-readable text)")
	flag.Parse()

	// Structured logging: one JSON object per line for shipping/jq/grep,
	// leveled via -log-level; every request line carries a correlation id
	// (X-Request-Id) so a client complaint maps to exactly one server trace.
	var lvl slog.Level
	if err := lvl.UnmarshalText([]byte(strings.ToLower(*logLevel))); err != nil {
		fmt.Fprintf(os.Stderr, "bad -log-level %q (want debug|info|warn|error)\n", *logLevel)
		os.Exit(2)
	}
	opts := &slog.HandlerOptions{Level: &lvl}
	var handler slog.Handler
	if *logJSON {
		handler = slog.NewJSONHandler(os.Stderr, opts)
	} else {
		handler = slog.NewTextHandler(os.Stderr, opts)
	}
	log := slog.New(handler)
	slog.SetDefault(log)

	conn, err := db.Open("sqlite", *dbPath)
	if err != nil {
		log.Error("open database", "path", *dbPath, "err", err)
		os.Exit(1)
	}
	defer conn.Close()

	tree, err := fsx.New(*root)
	if err != nil {
		log.Error("open root dir", "path", *root, "err", err)
		os.Exit(1)
	}
	log.Info("file root", "path", tree.Root())

	authStore := auth.NewStore(conn)
	if err := bootstrapAdmin(authStore, *adminFlag); err != nil {
		log.Error("bootstrap admin", "err", err)
		os.Exit(1)
	}

	api := handlers.API{
		Auth:       authStore,
		ACL:        acl.NewStore(conn),
		Events:     audit.NewStore(conn),
		Drafts:     drafts.NewStore(conn),
		FS:         tree,
		SessionTTL: 24 * time.Hour,
		MaxFileMB:  *maxMB,
		RPS:        *rps,
		RBurst:     *rburst,
		AuthRPS:    *authRPS,
		AuthBurst:  *authBurst,
	}
	mux := http.NewServeMux()
	api.Register(mux)

	if *webDir != "" {
		if info, err := os.Stat(*webDir); err == nil && info.IsDir() {
			mux.Handle("GET /", cacheStatic(http.FileServer(http.Dir(*webDir))))
			log.Info("web gui enabled", "dir", *webDir)
		} else if gui := frontend.BundleFS(); gui != nil {
			// release build (-tags embedweb): the GUI travels inside the
			// binary, so an unpacked bundle needs no -web and no web/ dir.
			mux.Handle("GET /", cacheStatic(http.FileServerFS(gui)))
			log.Info("web gui enabled", "dir", "(embedded)")
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

// bootstrapAdmin creates the first tenant and its super admin when the users
// table is empty. spec format: tenant:user:pass
func bootstrapAdmin(s *auth.Store, spec string) error {
	var count int
	if err := s.DB.QueryRow("SELECT COUNT(*) FROM users").Scan(&count); err != nil {
		return err
	}
	if count > 0 {
		return nil
	}
	tenant, rest, ok1 := strings.Cut(spec, ":")
	user, pass, ok2 := strings.Cut(rest, ":")
	if !ok1 || !ok2 || tenant == "" || user == "" || pass == "" {
		return fmt.Errorf("ADMIN/bootstrap must look like tenant:user:pass, got %q", spec)
	}
	ctx := context.Background()
	if _, err := s.CreateTenant(ctx, tenant, "bootstrap tenant"); err != nil {
		return fmt.Errorf("create tenant: %w", err)
	}
	if _, err := s.CreateUser(ctx, tenant, user, pass, "bootstrap super admin", true, true, nil); err != nil {
		return err
	}
	slog.Warn("bootstrap super admin created — change the password in production",
		"tenant", tenant, "username", user)
	return nil
}
