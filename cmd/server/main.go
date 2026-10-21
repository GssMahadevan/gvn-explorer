package main

import (
	"context"
	"crypto/tls"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/GssMahadevan/gvn-explorer/frontend"
	"github.com/GssMahadevan/gvn-explorer/internal/acl"
	"github.com/GssMahadevan/gvn-explorer/internal/audit"
	"github.com/GssMahadevan/gvn-explorer/internal/auth"
	"github.com/GssMahadevan/gvn-explorer/internal/db"
	"github.com/GssMahadevan/gvn-explorer/internal/drafts"
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
	authConcurrency := flag.Int("auth-concurrency", envIntOr("AUTH_CONCURRENCY", 4), "simultaneous bcrypt operations allowed (0 disables the cap)")
	logLevel := flag.String("log-level", envOr("LOG_LEVEL", "info"), "log level: debug|info|warn|error")
	logJSON := flag.Bool("log-json", envOr("LOG_JSON", "true") == "true", "emit JSON logs (false = human-readable text)")
	tlsCert := flag.String("tls-cert", envOr("TLS_CERT", ""), "TLS certificate file; with -tls-key serves HTTPS on -addr instead of the default self-signed cert")
	tlsKey := flag.String("tls-key", envOr("TLS_KEY", ""), "TLS private key file")
	tlsSelf := flag.Bool("tls-self", envOr("TLS_SELF", "true") == "true", "HTTPS with a generated in-memory self-signed cert — ON by default; pass -tls-self=false (or TLS_SELF=false) for plain HTTP")
	cookieSecure := flag.String("cookie-secure", envOr("COOKIE_SECURE", "auto"), "session cookie Secure attribute: auto (on when serving TLS) | on | off — set 'on' behind a TLS-terminating proxy")
	hstsDays := flag.Int("hsts-days", envIntOr("HSTS_DAYS", 0), "emit Strict-Transport-Security (max-age=N days) on TLS responses; 0 disables")
	flag.Parse()

	switch strings.ToLower(*cookieSecure) {
	case "auto", "on", "off":
	default:
		fmt.Fprintf(os.Stderr, "bad -cookie-secure %q (want auto|on|off)\n", *cookieSecure)
		os.Exit(2)
	}

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

	// TLS is the default: without any flag the server generates a
	// self-signed cert in memory (-tls-self defaults true). Explicit
	// files win; -tls-self=false opts out to plain HTTP, which stays
	// byte-identical to the pre-TLS server.
	var tlsPair *tls.Certificate
	switch {
	case *tlsCert != "" || *tlsKey != "":
		if *tlsCert == "" || *tlsKey == "" {
			fmt.Fprintln(os.Stderr, "-tls-cert and -tls-key must be given together")
			os.Exit(2)
		}
		c, err := tls.LoadX509KeyPair(*tlsCert, *tlsKey)
		if err != nil {
			log.Error("load TLS keypair", "cert", *tlsCert, "key", *tlsKey, "err", err)
			os.Exit(1)
		}
		tlsPair = &c
	case *tlsSelf:
		c, err := selfSignedCert()
		if err != nil {
			log.Error("generate self-signed cert", "err", err)
			os.Exit(1)
		}
		tlsPair = &c
		log.Info("serving HTTPS with an ephemeral self-signed certificate: browsers warn once; scripts/gen-selfsigned.sh makes a stable one, -tls-self=false serves plain HTTP")
	}
	// 'auto' follows the listener: cookies only claim Secure when they
	// really travel over TLS, so plain-HTTP dev setups keep working.
	cookieSecureOn := strings.EqualFold(*cookieSecure, "on") ||
		(strings.EqualFold(*cookieSecure, "auto") && tlsPair != nil)

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
		DB:        conn,
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

		AuthConcurrency: *authConcurrency,
	}
	api.CookieSecure = cookieSecureOn
	// Tenant homes: provision legacy tenants and the shared-root world
	// grant once, before any request is served (see API.BootstrapHomes).
	if err := api.BootstrapHomes(context.Background()); err != nil {
		log.Error("home bootstrap", "err", err)
		os.Exit(1)
	}
	mux := http.NewServeMux()
	api.Register(mux)

	if *webDir != "" {
		if info, err := os.Stat(*webDir); err == nil && info.IsDir() {
			mux.Handle("GET /", cacheStatic(http.FileServer(http.Dir(*webDir))))
			// /join is a GUI route: the pairing page lives inside the
			// bundle, so serve it the same index.html as '/'.
			mux.HandleFunc("GET /join", func(w http.ResponseWriter, r *http.Request) {
				http.ServeFile(w, r, filepath.Join(*webDir, "index.html"))
			})
			log.Info("web gui enabled", "dir", *webDir)
		} else if gui := frontend.BundleFS(); gui != nil {
			// release build (-tags embedweb): the GUI travels inside the
			// binary, so an unpacked bundle needs no -web and no web/ dir.
			mux.Handle("GET /", cacheStatic(http.FileServerFS(gui)))
			mux.HandleFunc("GET /join", func(w http.ResponseWriter, r *http.Request) {
				http.ServeFileFS(w, r, gui, "index.html")
			})
			log.Info("web gui enabled", "dir", "(embedded)")
		} else {
			log.Warn("web dir missing, GUI disabled", "dir", *webDir)
		}
	}

	srv := &http.Server{
		Addr:              *addr,
		Handler:           securityHeaders(*hstsDays, withMiddleware(log, mux)),
		ReadHeaderTimeout: 5 * time.Second,
	}

	go func() {
		if tlsPair != nil {
			ln, err := net.Listen("tcp", *addr)
			if err != nil {
				log.Error("listen", "addr", *addr, "err", err)
				os.Exit(1)
			}
			srv.TLSConfig = &tls.Config{
				MinVersion:   tls.VersionTLS12,
				Certificates: []tls.Certificate{*tlsPair},
			}
			log.Info("server starting (https)", "addr", *addr, "note", "plain-HTTP requests on this port get a 308 to https")
			if err := srv.ServeTLS(httpsRedirectListener(ln), "", ""); err != nil && !errors.Is(err, http.ErrServerClosed) {
				log.Error("server error", "err", err)
				os.Exit(1)
			}
			return
		}
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
