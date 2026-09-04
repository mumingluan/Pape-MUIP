package app

import (
	"database/sql"
	"embed"
	"fmt"
	"html/template"
	"io/fs"
	"log"
	"net/http"
	"path/filepath"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	_ "modernc.org/sqlite"

	"pape-muip/internal/config"
)

//go:embed web/*
var webFiles embed.FS

type App struct {
	cfg           config.Config
	language      *sql.DB
	clientsMu     sync.Mutex
	clients       map[string]*http.Client
	sessionsMu    sync.Mutex
	sessions      map[string]time.Time
	loginTemplate *template.Template
}

func Run(configPath string) error {
	cfg, err := config.Load(configPath)
	if err != nil {
		return err
	}
	language, err := openLanguage(cfg.Resolve(cfg.LanguageDB))
	if err != nil {
		return err
	}
	defer language.Close()
	server := &App{cfg: *cfg, language: language, clients: map[string]*http.Client{}}
	log.Printf("Pape-MUIP listening on http://%s", cfg.Listen)
	return (&http.Server{Addr: cfg.Listen, Handler: server.router(), ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout: 30 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: 90 * time.Second}).ListenAndServe()
}

func openLanguage(path string) (*sql.DB, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(abs)+"?mode=ro&immutable=1")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(4)
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("open language database: %w", err)
	}
	return db, nil
}

func (s *App) router() *gin.Engine {
	gin.SetMode(gin.ReleaseMode)
	router := gin.New()
	router.Use(gin.Logger(), gin.Recovery(), s.securityHeaders(), gzipResponses())
	content, err := fs.Sub(webFiles, "web")
	if err != nil {
		panic(fmt.Sprintf("load embedded web files: %v", err))
	}
	s.loginTemplate = template.Must(template.ParseFS(webFiles, "web/login.html"))
	if s.sessions == nil {
		s.sessions = make(map[string]time.Time)
	}
	publicFiles := http.FS(content)
	router.GET("/login", s.loginPage)
	router.POST("/login", s.login)
	router.GET("/style.css", func(c *gin.Context) { c.FileFromFS("style.css", publicFiles) })

	protected := router.Group("/", s.requireSession())
	protected.POST("/logout", s.logout)
	protected.GET("/api/health", s.health)
	protected.Any("/api/sdk/*path", s.sdkProxy)
	protected.Any("/api/booi/:server/*path", s.booiProxy)
	protected.POST("/api/operations/booi/:server/players/:id/full-catalog", s.fullCatalogPlayer)
	protected.POST("/api/operations/booi/:server/players/:id/import-sync", s.importSyncPlayer)
	protected.GET("/api/language/:id", s.languageLookup)
	router.NoRoute(s.requireSession(), gin.WrapH(http.FileServer(publicFiles)))
	return router
}
