package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/t0ggl/callimachus/pkg/auth"
	"github.com/t0ggl/callimachus/pkg/catalog"
	"github.com/t0ggl/callimachus/pkg/database"
	"github.com/t0ggl/callimachus/pkg/downloaders"
	"github.com/t0ggl/callimachus/pkg/indexers"
	"github.com/t0ggl/callimachus/pkg/notify"
	"github.com/t0ggl/callimachus/pkg/requests"
	"github.com/t0ggl/callimachus/pkg/transcoder"
	"github.com/t0ggl/callimachus/web"
)

var (
	version = "dev"
	commit  = "none"
)

// Config holds runtime configuration options for the Callimachus server.
type Config struct {
	ConfigDir           string
	DBPath              string
	LibraryPath         string
	Host                string
	Port                int
	StaticDir           string
	StaticFS            fs.FS
	ProwlarrURL         string
	ProwlarrAPIKey      string
	ProwlarrInterval    time.Duration
	IndexerSyncInterval time.Duration
	StagingPollInterval time.Duration
	Version             string
	Commit              string

	// OIDC & Auth Configuration
	OIDCIssuerURL     string
	OIDCClientID      string
	OIDCClientSecret  string
	OIDCRedirectURL   string
	OIDCAdminGroups   []string
	OIDCCuratorGroups []string
	OIDCDefaultRole   string
	FallbackPassword  string
}

// Server coordinates the database, background workers, and HTTP services.
type Server struct {
	cfg        Config
	db         *database.DB
	sessionMgr *auth.SessionManager
	syncer     *indexers.Syncer
	stagingMon *downloaders.StagingMonitor
	dispatcher *notify.Dispatcher
	httpServer *http.Server
	listener   net.Listener
	staticFS   fs.FS
}

// NewServer initializes a new Server instance with all components and routes configured.
func NewServer(cfg Config) (*Server, error) {
	if cfg.Version == "" {
		cfg.Version = version
	}
	if cfg.Commit == "" {
		cfg.Commit = commit
	}

	dbPath := cfg.DBPath
	if dbPath == "" {
		dbPath = os.Getenv("DATABASE_PATH")
	}
	if dbPath == "" && cfg.ConfigDir != "" {
		dbPath = filepath.Join(cfg.ConfigDir, "callimachus.db")
	}
	if dbPath == "" {
		dbPath = "callimachus.db"
	}
	cfg.DBPath = dbPath

	libraryPath := cfg.LibraryPath
	if libraryPath == "" {
		libraryPath = os.Getenv("BOOKS_DIR")
	}
	if libraryPath == "" {
		libraryPath = os.Getenv("LIBRARY_PATH")
	}
	if libraryPath == "" {
		if _, err := os.Stat("/books"); err == nil {
			libraryPath = "/books"
		} else {
			libraryPath = "./books"
		}
	}
	cfg.LibraryPath = libraryPath

	if cfg.IndexerSyncInterval <= 0 {
		cfg.IndexerSyncInterval = 15 * time.Minute
	}
	if cfg.StagingPollInterval <= 0 {
		cfg.StagingPollInterval = 5 * time.Second
	}
	if cfg.ProwlarrInterval <= 0 {
		cfg.ProwlarrInterval = 1 * time.Hour
	}

	// 1. Initialize SQLite Database & Schema Migrations
	db, err := database.Open(database.Config{Path: cfg.DBPath})
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}

	migrateCtx, migrateCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer migrateCancel()
	if err := db.Migrate(migrateCtx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("database migration: %w", err)
	}

	// 2. Auth & Session Management
	sessionMgr := auth.NewSessionManager(24*time.Hour, auth.DefaultCookieConfig())

	var oidcClient *auth.OIDCClient
	if cfg.OIDCIssuerURL != "" && cfg.OIDCClientID != "" {
		oc, err := auth.NewOIDCClient(auth.OIDCConfig{
			Issuer:        cfg.OIDCIssuerURL,
			ClientID:      cfg.OIDCClientID,
			ClientSecret:  cfg.OIDCClientSecret,
			RedirectURL:   cfg.OIDCRedirectURL,
			AdminGroups:   cfg.OIDCAdminGroups,
			CuratorGroups: cfg.OIDCCuratorGroups,
			DefaultRole:   cfg.OIDCDefaultRole,
		})
		if err != nil {
			log.Printf("[auth] warning: failed to initialize OIDC client: %v", err)
		} else {
			oidcClient = oc
			log.Printf("[auth] OIDC client configured (issuer: %s, clientID: %s)", cfg.OIDCIssuerURL, cfg.OIDCClientID)
		}
	}

	var fallbackAuth *auth.FallbackAuthenticator
	if cfg.FallbackPassword != "" {
		fallbackAuth = auth.NewFallbackAuthenticator(auth.FallbackConfig{AdminPassword: cfg.FallbackPassword}, db, sessionMgr)
		log.Printf("[auth] single password fallback authentication enabled")
	}

	authHandler := auth.NewAuthHandler(auth.HandlerConfig{
		SessionManager: sessionMgr,
		DB:             db,
		OIDCClient:     oidcClient,
		FallbackAuth:   fallbackAuth,
	})

	// 3. User Book Requests Workflow
	reqService := requests.NewRequestService(db)
	reqHandler := requests.NewHandler(requests.HandlerConfig{
		Service:        reqService,
		SessionManager: sessionMgr,
		DB:             db,
	})

	// 4. Indexers Syncer & Prowlarr Integration
	syncer := indexers.NewSyncer(db, indexers.SyncerConfig{
		Interval: cfg.IndexerSyncInterval,
	})

	// 5. Download Staging Monitor
	stagingMon := downloaders.NewStagingMonitor(downloaders.StagingConfig{
		DB:           db,
		Catalog:      catalog.New(db),
		Transcoder:   transcoder.NewQueue(db),
		LibraryRoot:  cfg.LibraryPath,
		PollInterval: cfg.StagingPollInterval,
	})

	// 6. Notification Dispatcher
	dispatcher := notify.NewDispatcher()

	// 7. Determine Static Assets Filesystem
	var staticFS fs.FS
	if cfg.StaticFS != nil {
		staticFS = cfg.StaticFS
	} else if cfg.StaticDir != "" {
		if fi, err := os.Stat(cfg.StaticDir); err == nil && fi.IsDir() {
			staticFS = os.DirFS(cfg.StaticDir)
		}
	}
	if staticFS == nil {
		if sub, err := web.FS(); err == nil {
			staticFS = sub
		}
	}

	srv := &Server{
		cfg:        cfg,
		db:         db,
		sessionMgr: sessionMgr,
		syncer:     syncer,
		stagingMon: stagingMon,
		dispatcher: dispatcher,
		staticFS:   staticFS,
	}

	// 8. Register HTTP Endpoints & Router
	mux := http.NewServeMux()

	// Health check endpoints
	mux.HandleFunc("/healthz", srv.handleHealth)
	mux.HandleFunc("/api/health", srv.handleHealth)

	// Auth and Requests APIs
	authHandler.RegisterRoutes(mux)
	reqHandler.RegisterRoutes(mux)

	// SPA router handles all non-API paths
	mux.HandleFunc("/", srv.handleSPA)

	srv.httpServer = &http.Server{
		Handler:      mux,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 60 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	return srv, nil
}

// Start begins the HTTP server listener and all background worker tasks.
func (s *Server) Start(ctx context.Context) error {
	addr := fmt.Sprintf("%s:%d", s.cfg.Host, s.cfg.Port)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", addr, err)
	}
	s.listener = ln

	// Start background indexer syncer
	s.syncer.Start(ctx)

	// Start background staging monitor
	if err := s.stagingMon.Start(ctx); err != nil {
		log.Printf("[staging] warning: failed to start staging monitor: %v", err)
	}

	// Start background Prowlarr sync if configured
	if s.cfg.ProwlarrURL != "" {
		go s.runProwlarrSync(ctx)
	}

	// Run HTTP listener in background
	go func() {
		if err := s.httpServer.Serve(s.listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Printf("[http] server error: %v", err)
		}
	}()

	return nil
}

// Addr returns the bound TCP network address string of the HTTP server.
func (s *Server) Addr() string {
	if s.listener != nil {
		return s.listener.Addr().String()
	}
	return fmt.Sprintf("%s:%d", s.cfg.Host, s.cfg.Port)
}

// Shutdown gracefully stops the HTTP server and all background services.
func (s *Server) Shutdown(ctx context.Context) error {
	var errs []error

	if s.httpServer != nil {
		if err := s.httpServer.Shutdown(ctx); err != nil {
			errs = append(errs, fmt.Errorf("http shutdown: %w", err))
		}
	}

	if s.stagingMon != nil {
		s.stagingMon.Stop()
	}

	if s.syncer != nil {
		s.syncer.Stop()
	}

	if s.dispatcher != nil {
		if err := s.dispatcher.Close(); err != nil {
			errs = append(errs, fmt.Errorf("dispatcher close: %w", err))
		}
	}

	if s.db != nil {
		if err := s.db.Close(); err != nil {
			errs = append(errs, fmt.Errorf("database close: %w", err))
		}
	}

	if len(errs) > 0 {
		return errors.Join(errs...)
	}
	return nil
}

func (s *Server) runProwlarrSync(ctx context.Context) {
	client := indexers.NewProwlarrClient(s.cfg.ProwlarrURL, s.cfg.ProwlarrAPIKey, nil)
	ticker := time.NewTicker(s.cfg.ProwlarrInterval)
	defer ticker.Stop()

	// Initial sync pass
	if _, err := client.SyncIndexers(ctx, s.db); err != nil {
		log.Printf("[prowlarr] initial sync error: %v", err)
	}

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if _, err := client.SyncIndexers(ctx, s.db); err != nil {
				log.Printf("[prowlarr] periodic sync error: %v", err)
			}
		}
	}
}

// HealthResponse represents the payload returned by health check endpoints.
type HealthResponse struct {
	Status string `json:"status"`
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	status := "ok"
	httpStatus := http.StatusOK

	if s.db != nil {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		if err := s.db.PingContext(ctx); err != nil {
			status = "not ok"
			httpStatus = http.StatusServiceUnavailable
		}
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(httpStatus)
	_ = json.NewEncoder(w).Encode(HealthResponse{Status: status})
}

func (s *Server) handleSPA(w http.ResponseWriter, r *http.Request) {
	// Unknown API requests must return 404 JSON, never SPA HTML
	if strings.HasPrefix(r.URL.Path, "/api/") {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"error": "api endpoint not found",
		})
		return
	}

	cleanPath := path.Clean(strings.TrimPrefix(r.URL.Path, "/"))
	if cleanPath == "." || cleanPath == "" || cleanPath == "/" {
		cleanPath = "index.html"
	}

	// Serve static asset file if present in staticFS
	if s.staticFS != nil && cleanPath != "index.html" {
		if f, err := s.staticFS.Open(cleanPath); err == nil {
			defer f.Close()
			if st, err := f.Stat(); err == nil && !st.IsDir() {
				http.FileServer(http.FS(s.staticFS)).ServeHTTP(w, r)
				return
			}
		}
	}

	// SPA navigation routes: serve index.html from staticFS
	if s.staticFS != nil {
		content, err := fs.ReadFile(s.staticFS, "index.html")
		if err == nil && len(content) > 0 {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(content)
			return
		}
	}

	// Fallback to minimal built-in HTML if dist/index.html is missing/empty
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(web.FallbackHTML))
}

func run(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("callimachus", flag.ContinueOnError)
	configFlag := fs.String("config", "", "Path to Callimachus configuration directory (default: /config or .)")
	dbFlag := fs.String("db", "", "Path to Callimachus SQLite database (default: $DATABASE_PATH or <config>/callimachus.db)")
	libraryFlag := fs.String("library", "", "Path to root books library directory (default: $BOOKS_DIR or /books)")
	hostFlag := fs.String("host", "0.0.0.0", "HTTP host to bind (default: 0.0.0.0)")
	portFlag := fs.Int("port", 8080, "HTTP port to listen on (default: 8080)")
	webDirFlag := fs.String("web-dir", "", "Path to custom web distribution directory (default: embedded web/dist)")
	prowlarrURLFlag := fs.String("prowlarr-url", "", "Prowlarr base URL (default: $PROWLARR_URL)")
	prowlarrKeyFlag := fs.String("prowlarr-key", "", "Prowlarr API key (default: $PROWLARR_API_KEY)")
	oidcIssuerFlag := fs.String("oidc-issuer", "", "OIDC issuer URL (default: $OIDC_ISSUER_URL or $OIDC_ISSUER)")
	oidcClientIDFlag := fs.String("oidc-client-id", "", "OIDC client ID (default: $OIDC_CLIENT_ID)")
	oidcClientSecretFlag := fs.String("oidc-client-secret", "", "OIDC client secret (default: $OIDC_CLIENT_SECRET)")
	oidcRedirectURLFlag := fs.String("oidc-redirect-url", "", "OIDC redirect URL (default: $OIDC_REDIRECT_URL)")
	oidcAdminGroupsFlag := fs.String("oidc-admin-groups", "", "Comma-separated OIDC groups mapped to admin role (default: $OIDC_ADMIN_GROUPS)")
	oidcCuratorGroupsFlag := fs.String("oidc-curator-groups", "", "Comma-separated OIDC groups mapped to curator role (default: $OIDC_CURATOR_GROUPS)")
	oidcDefaultRoleFlag := fs.String("oidc-default-role", "", "Default role for new OIDC users (default: $OIDC_DEFAULT_ROLE)")
	authPasswordFlag := fs.String("auth-password", "", "Single password fallback authentication (default: $AUTH_PASSWORD or $ADMIN_PASSWORD)")

	if err := fs.Parse(args); err != nil {
		return err
	}

	configDir := *configFlag
	if configDir == "" {
		configDir = os.Getenv("CONFIG_DIR")
	}
	if configDir == "" {
		if fi, err := os.Stat("/config"); err == nil && fi.IsDir() {
			configDir = "/config"
		} else {
			configDir = "."
		}
	}

	dbPath := *dbFlag
	if dbPath == "" {
		dbPath = os.Getenv("DATABASE_PATH")
	}
	if dbPath == "" && configDir != "" {
		dbPath = filepath.Join(configDir, "callimachus.db")
	}

	prowlarrURL := *prowlarrURLFlag
	if prowlarrURL == "" {
		prowlarrURL = os.Getenv("PROWLARR_URL")
	}
	prowlarrKey := *prowlarrKeyFlag
	if prowlarrKey == "" {
		prowlarrKey = os.Getenv("PROWLARR_API_KEY")
	}

	oidcIssuer := *oidcIssuerFlag
	if oidcIssuer == "" {
		oidcIssuer = os.Getenv("OIDC_ISSUER_URL")
	}
	if oidcIssuer == "" {
		oidcIssuer = os.Getenv("OIDC_ISSUER")
	}

	oidcClientID := *oidcClientIDFlag
	if oidcClientID == "" {
		oidcClientID = os.Getenv("OIDC_CLIENT_ID")
	}

	oidcClientSecret := *oidcClientSecretFlag
	if oidcClientSecret == "" {
		oidcClientSecret = os.Getenv("OIDC_CLIENT_SECRET")
	}

	oidcRedirectURL := *oidcRedirectURLFlag
	if oidcRedirectURL == "" {
		oidcRedirectURL = os.Getenv("OIDC_REDIRECT_URL")
	}

	oidcAdminGroupsStr := *oidcAdminGroupsFlag
	if oidcAdminGroupsStr == "" {
		oidcAdminGroupsStr = os.Getenv("OIDC_ADMIN_GROUPS")
	}

	oidcCuratorGroupsStr := *oidcCuratorGroupsFlag
	if oidcCuratorGroupsStr == "" {
		oidcCuratorGroupsStr = os.Getenv("OIDC_CURATOR_GROUPS")
	}

	oidcDefaultRole := *oidcDefaultRoleFlag
	if oidcDefaultRole == "" {
		oidcDefaultRole = os.Getenv("OIDC_DEFAULT_ROLE")
	}

	authPassword := *authPasswordFlag
	if authPassword == "" {
		authPassword = os.Getenv("AUTH_PASSWORD")
	}
	if authPassword == "" {
		authPassword = os.Getenv("ADMIN_PASSWORD")
	}

	splitComma := func(s string) []string {
		if s == "" {
			return nil
		}
		var parts []string
		for _, p := range strings.Split(s, ",") {
			if t := strings.TrimSpace(p); t != "" {
				parts = append(parts, t)
			}
		}
		return parts
	}

	cfg := Config{
		ConfigDir:           configDir,
		DBPath:              dbPath,
		LibraryPath:         *libraryFlag,
		Host:                *hostFlag,
		Port:                *portFlag,
		StaticDir:           *webDirFlag,
		ProwlarrURL:         prowlarrURL,
		ProwlarrAPIKey:      prowlarrKey,
		Version:             version,
		Commit:              commit,
		OIDCIssuerURL:       oidcIssuer,
		OIDCClientID:        oidcClientID,
		OIDCClientSecret:    oidcClientSecret,
		OIDCRedirectURL:     oidcRedirectURL,
		OIDCAdminGroups:     splitComma(oidcAdminGroupsStr),
		OIDCCuratorGroups:   splitComma(oidcCuratorGroupsStr),
		OIDCDefaultRole:     oidcDefaultRole,
		FallbackPassword:    authPassword,
	}

	srv, err := NewServer(cfg)
	if err != nil {
		return fmt.Errorf("create server: %w", err)
	}

	if err := srv.Start(ctx); err != nil {
		return fmt.Errorf("start server: %w", err)
	}

	log.Printf("[callimachus] Callimachus %s (commit %s) listening on %s", version, commit, srv.Addr())

	<-ctx.Done()
	log.Printf("[callimachus] Shutting down gracefully...")

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer shutdownCancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("shutdown: %w", err)
	}

	log.Printf("[callimachus] Server stopped cleanly.")
	return nil
}

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	if err := run(ctx, os.Args[1:]); err != nil && !errors.Is(err, context.Canceled) {
		log.Fatalf("[callimachus] fatal error: %v", err)
	}
}
