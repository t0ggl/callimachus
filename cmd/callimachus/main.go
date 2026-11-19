package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/t0ggl/callimachus/internal/logging"
	"github.com/t0ggl/callimachus/pkg/auth"
	"github.com/t0ggl/callimachus/pkg/catalog"
	"github.com/t0ggl/callimachus/pkg/config"
	"github.com/t0ggl/callimachus/pkg/converter"
	"github.com/t0ggl/callimachus/pkg/database"
	"github.com/t0ggl/callimachus/pkg/downloaders"
	"github.com/t0ggl/callimachus/pkg/indexers"
	"github.com/t0ggl/callimachus/pkg/notify"
	"github.com/t0ggl/callimachus/pkg/requests"
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
}

// Server coordinates the database, background workers, and HTTP services.
type Server struct {
	catalogMonitor     *catalog.Monitor
	monitorDone        chan struct{}
	monitorWake        chan struct{}
	acquisition        *requests.Acquisition
	acquisitionCancel  context.CancelFunc
	acquisitionDone    chan struct{}
	cfg                Config
	baseOverrides      config.Overrides
	settingsMu         sync.Mutex
	db                 *database.DB
	sessionMgr         *auth.SessionManager
	maintenance        config.MaintenanceConfig
	maintenanceDone    chan struct{}
	fallbackAuth       *auth.FallbackAuthenticator
	syncer             *indexers.Syncer
	prowlarrHTTPClient *http.Client
	stagingMon         *downloaders.StagingMonitor
	dispatcher         *notify.Dispatcher
	httpServer         *http.Server
	listener           net.Listener
	staticFS           fs.FS
}

// NewServer initializes a new Server instance with all components and routes configured.
func NewServer(cfg Config) (*Server, error) {
	if cfg.Version == "" {
		cfg.Version = version
	}
	if cfg.Commit == "" {
		cfg.Commit = commit
	}

	maintenance, err := config.LoadMaintenanceFromEnv()
	if err != nil {
		return nil, err
	}

	boot := config.ResolveBootstrap(cfg.ConfigDir, cfg.DBPath, cfg.Host, cfg.Port, true)
	cfg.ConfigDir = boot.ConfigDir
	cfg.DBPath = boot.DBPath
	cfg.Host = boot.Host
	cfg.Port = boot.Port

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

	// Resolve effective non-auth configuration (Env > Caller Overrides > SQLite settings > Defaults)
	baseOverrides := config.Overrides{
		LibraryRoot:          cfg.LibraryPath,
		ProwlarrURL:          cfg.ProwlarrURL,
		ProwlarrAPIKey:       cfg.ProwlarrAPIKey,
		ProwlarrSyncInterval: cfg.ProwlarrInterval,
		IndexerSyncInterval:  cfg.IndexerSyncInterval,
		StagingPollInterval:  cfg.StagingPollInterval,
	}

	resolved, err := config.Load(migrateCtx, db, baseOverrides)
	if err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("load configuration: %w", err)
	}

	sCfg := resolved.Settings
	logging.Level.Set(sCfg.LogLevel)
	cfg.LibraryPath = sCfg.LibraryRoot
	cfg.ProwlarrURL = sCfg.ProwlarrURL
	cfg.ProwlarrAPIKey = sCfg.ProwlarrAPIKey
	cfg.ProwlarrInterval = sCfg.ProwlarrSyncInterval
	cfg.IndexerSyncInterval = sCfg.IndexerSyncInterval
	cfg.StagingPollInterval = sCfg.StagingPollInterval

	// 2. Auth & Session Management (exclusively from environment variables)
	authCfg := config.LoadAuthFromEnv()
	sessionTTL := time.Duration(authCfg.SessionTimeoutHours) * time.Hour
	if sessionTTL <= 0 {
		sessionTTL = 24 * time.Hour
	}
	sessionMgr := auth.NewSessionManager(sessionTTL, auth.DefaultCookieConfig())

	var oidcClient *auth.OIDCClient
	oidcReady, oidcWarnings := authCfg.ValidateOIDC()
	for _, w := range oidcWarnings {
		slog.Warn(w)
	}
	if oidcReady {
		oc, err := auth.NewOIDCClient(auth.OIDCConfig{
			Issuer:        authCfg.OIDCIssuerURL,
			ClientID:      authCfg.OIDCClientID,
			ClientSecret:  authCfg.OIDCClientSecret,
			RedirectURL:   authCfg.OIDCRedirectURL,
			Scopes:        authCfg.OIDCScopes,
			AdminGroups:   authCfg.OIDCAdminGroups,
			CuratorGroups: authCfg.OIDCCuratorGroups,
			DefaultRole:   authCfg.OIDCDefaultRole,
		})
		if err != nil {
			slog.Warn("OIDC client initialization failed")
		} else {
			oidcClient = oc
			slog.Info("OIDC authentication configured")
		}
	}

	var fallbackAuth *auth.FallbackAuthenticator
	if authCfg.AdminPassword != "" {
		fallbackAuth = auth.NewFallbackAuthenticator(auth.FallbackConfig{AdminPassword: authCfg.AdminPassword}, db, sessionMgr)
		slog.Info("password authentication enabled")
	}

	authHandler := auth.NewAuthHandler(auth.HandlerConfig{
		SessionManager: sessionMgr,
		DB:             db,
		OIDCClient:     oidcClient,
		FallbackAuth:   fallbackAuth,
	})

	// 3. User Book Requests Workflow
	reqService := requests.NewRequestService(db)

	// 4. Indexers Syncer & Prowlarr Integration
	syncer := indexers.NewSyncer(db, indexers.SyncerConfig{
		Interval: sCfg.IndexerSyncInterval,
	})

	// 5. Download Staging Monitor
	stagingMon := downloaders.NewStagingMonitor(downloaders.StagingConfig{
		QualityProfile: func(ctx context.Context) (downloaders.QualityProfile, error) {
			resolved, err := config.Load(ctx, db, config.Overrides{})
			if err != nil {
				return downloaders.QualityProfile{}, err
			}
			return resolved.Settings.ToQualityProfile(), nil
		},
		DB:            db,
		Catalog:       catalog.New(db),
		Converter:     converter.NewQueue(db),
		LibraryRoot:   sCfg.LibraryRoot,
		CompletedRoot: sCfg.CompletedDownloadRoot,
		PollInterval:  sCfg.StagingPollInterval,
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
		maintenance:   maintenance,
		cfg:           cfg,
		baseOverrides: baseOverrides,
		db:            db,
		sessionMgr:    sessionMgr,
		fallbackAuth:  fallbackAuth,
		syncer:        syncer,
		stagingMon:    stagingMon,
		dispatcher:    dispatcher,
		staticFS:      staticFS,
	}

	srv.acquisition = srv.newAcquisition()
	srv.catalogMonitor = srv.newCatalogMonitor()
	srv.monitorWake = make(chan struct{}, 1)
	reqHandler := requests.NewHandler(requests.HandlerConfig{Service: reqService, SessionManager: sessionMgr, DB: db, Acquisition: srv.acquisition})

	// 8. Register HTTP Endpoints & Router
	mux := http.NewServeMux()

	// Health check endpoints
	mux.HandleFunc("/healthz", srv.handleHealth)
	mux.HandleFunc("/api/health", srv.handleHealth)

	// Auth, Requests, and Catalog APIs
	authHandler.RegisterRoutes(mux)
	reqHandler.RegisterRoutes(mux)
	srv.registerAPIRoutes(mux)

	// SPA router handles all non-API paths
	mux.HandleFunc("/", srv.handleSPA)

	handler := auth.Authenticate(sessionMgr, db)(srv.withAuthGate(mux))

	srv.httpServer = &http.Server{
		Handler:      handler,
		ErrorLog:     log.New(httpErrorWriter{}, "", 0),
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

	workerCtx, cancel := context.WithCancel(ctx)
	s.acquisitionCancel = cancel
	s.monitorDone = make(chan struct{})
	go func() { defer close(s.monitorDone); s.runCatalogMonitor(workerCtx) }()
	s.acquisitionDone = make(chan struct{})
	go func() {
		defer close(s.acquisitionDone)
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-workerCtx.Done():
				return
			case <-ticker.C:
				if err := s.acquisition.Tick(workerCtx); err != nil {
					slog.ErrorContext(workerCtx, "acquisition reconciliation failed")
				}
			}
		}
	}()

	s.maintenanceDone = make(chan struct{})
	go func() {
		defer close(s.maintenanceDone)
		s.runMaintenance(workerCtx)
	}()

	// Start background indexer syncer
	s.syncer.Start(ctx)

	// Start background staging monitor
	if err := s.stagingMon.Start(ctx); err != nil {
		slog.WarnContext(ctx, "staging monitor could not start")
	}

	// Start background Prowlarr sync if configured
	if s.cfg.ProwlarrURL != "" {
		go s.runProwlarrSync(ctx)
	}

	// Run HTTP listener in background
	go func() {
		if err := s.httpServer.Serve(s.listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.ErrorContext(ctx, "HTTP listener failed")
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
	if s.acquisitionCancel != nil {
		s.acquisitionCancel()
		<-s.acquisitionDone
		if s.monitorDone != nil {
			<-s.monitorDone
		}
		if s.maintenanceDone != nil {
			<-s.maintenanceDone
		}
	}
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
	_, _ = client.SyncIndexers(ctx, s.db)

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			_, _ = client.SyncIndexers(ctx, s.db)
		}
	}
}

// HealthResponse represents the payload returned by health check endpoints.
type HealthResponse struct {
	Status string `json:"status"`
}

func (s *Server) withAuthGate(next *http.ServeMux) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cleanPath := path.Clean("/" + r.URL.Path)
		isAPI := cleanPath == "/api" || strings.HasPrefix(cleanPath, "/api/")
		if isAPI && slog.Default().Enabled(r.Context(), slog.LevelDebug) {
			// Log the registered route, which omits query strings and wildcard values.
			route := "unmatched"
			// ServeMux can return the request path as the pattern for CONNECT redirects.
			if r.Method != http.MethodConnect {
				if _, pattern := next.Handler(r); pattern != "" && pattern != "/" {
					route = pattern
				}
			}
			slog.DebugContext(r.Context(), "API request", "route", route)
		}

		// Always allow public health probes and OIDC initiation/callback endpoints
		if cleanPath == "/healthz" ||
			cleanPath == "/api/health" ||
			cleanPath == "/api/auth/oidc/login" ||
			cleanPath == "/api/auth/oidc/callback" {
			next.ServeHTTP(w, r)
			return
		}

		// Allow fallback password login only when explicitly configured
		if cleanPath == "/api/auth/login" && r.Method == http.MethodPost &&
			s.fallbackAuth != nil && s.fallbackAuth.IsEnabled() {
			next.ServeHTTP(w, r)
			return
		}

		// Allow authenticated requests
		if user, ok := auth.UserFromContext(r.Context()); ok && user != nil {
			next.ServeHTTP(w, r)
			return
		}

		// Unauthenticated API requests must receive HTTP 401 Unauthorized JSON
		if isAPI {
			slog.DebugContext(r.Context(), "API request denied", "reason", "unauthenticated")
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":"unauthorized"}`))
			return
		}

		// Unauthenticated frontend/SPA requests redirect directly to OIDC login
		loginURL := "/api/auth/oidc/login"
		reqURI := r.URL.RequestURI()
		if cleanPath != "/" && auth.IsSafeRedirectTarget(reqURI) && !isStaticAssetPath(cleanPath) {
			loginURL += "?redirect=" + url.QueryEscape(reqURI)
		}
		// nosemgrep: go.lang.security.injection.open-redirect.open-redirect -- loginURL always targets local /api/auth/oidc/login with a validated query parameter
		http.Redirect(w, r, loginURL, http.StatusFound)
	})
}

func isStaticAssetPath(cleanPath string) bool {
	if strings.HasPrefix(cleanPath, "/assets/") {
		return true
	}
	switch strings.ToLower(path.Ext(cleanPath)) {
	case ".js", ".css", ".svg", ".png", ".jpg", ".jpeg", ".webp", ".ico", ".woff", ".woff2", ".ttf", ".map", ".html", ".txt", ".json":
		return true
	default:
		return false
	}
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

	cleanPath := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
	if cleanPath == "" {
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
			// nosemgrep: go.lang.security.audit.xss.no-direct-write-to-responsewriter.no-direct-write-to-responsewriter -- serving static SPA index.html
			_, _ = w.Write(content)
			return
		}
	}

	// Fallback to minimal built-in HTML if dist/index.html is missing/empty
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	// nosemgrep: go.lang.security.audit.xss.no-direct-write-to-responsewriter.no-direct-write-to-responsewriter -- serving static built-in FallbackHTML constant
	_, _ = w.Write([]byte(web.FallbackHTML))
}

func run(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("callimachus", flag.ContinueOnError)
	configFlag := fs.String("config", "", "Path to Callimachus configuration directory (default: $CALLIMACHUS_CONFIG_DIR, /config, or .)")
	dbFlag := fs.String("db", "", "Path to Callimachus SQLite database (default: $CALLIMACHUS_DATABASE_PATH or <config>/callimachus.db)")
	libraryFlag := fs.String("library", "", "Path to root books library directory (default: $CALLIMACHUS_LIBRARY_ROOT, /books, or ./books)")
	hostFlag := fs.String("host", "", "HTTP host to bind (default: $CALLIMACHUS_HOST or 0.0.0.0)")
	portFlag := fs.Int("port", 8080, "HTTP port to listen on (default: $CALLIMACHUS_PORT or 8080)")
	webDirFlag := fs.String("web-dir", "", "Path to custom web distribution directory (default: embedded web/dist)")
	prowlarrURLFlag := fs.String("prowlarr-url", "", "Prowlarr base URL (default: $CALLIMACHUS_PROWLARR_URL)")
	prowlarrKeyFlag := fs.String("prowlarr-key", "", "Prowlarr API key (default: $CALLIMACHUS_PROWLARR_API_KEY)")

	if err := fs.Parse(args); err != nil {
		return err
	}

	portSet := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "port" {
			portSet = true
		}
	})

	boot := config.ResolveBootstrap(*configFlag, *dbFlag, *hostFlag, *portFlag, portSet)

	cfg := Config{
		ConfigDir:      boot.ConfigDir,
		DBPath:         boot.DBPath,
		LibraryPath:    *libraryFlag,
		Host:           boot.Host,
		Port:           boot.Port,
		StaticDir:      *webDirFlag,
		ProwlarrURL:    *prowlarrURLFlag,
		ProwlarrAPIKey: *prowlarrKeyFlag,
		Version:        version,
		Commit:         commit,
	}

	srv, err := NewServer(cfg)
	if err != nil {
		return fmt.Errorf("create server: %w", err)
	}

	if err := srv.Start(ctx); err != nil {
		return fmt.Errorf("start server: %w", err)
	}

	slog.Info("server listening", "version", version, "commit", commit, "address", srv.Addr())

	<-ctx.Done()
	slog.Info("server shutting down")

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer shutdownCancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("shutdown: %w", err)
	}

	slog.Info("server stopped")
	return nil
}

// net/http diagnostics can contain arbitrary panic values or request data.
type httpErrorWriter struct{}

func (httpErrorWriter) Write(p []byte) (int, error) {
	slog.Error("HTTP server request handling failed")
	return len(p), nil
}

func main() {
	slog.SetDefault(logging.New(os.Stderr))
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	if err := run(ctx, os.Args[1:]); err != nil && !errors.Is(err, context.Canceled) {
		if errors.Is(err, config.ErrInvalidLogLevel) {
			slog.Error(config.ErrInvalidLogLevel.Error(), "setting", config.EnvVarName(config.KeyLogLevel))
		} else {
			slog.Error("server could not complete startup or shutdown")
		}
		os.Exit(1)
	}
}
