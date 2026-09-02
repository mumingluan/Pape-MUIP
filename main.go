package main

import (
	"database/sql"
	"embed"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"gopkg.in/yaml.v3"
	_ "modernc.org/sqlite"
)

//go:embed web/*
var webFiles embed.FS

type Peer struct {
	BaseURL        string `yaml:"base_url" json:"base_url"`
	AuthToken      string `yaml:"auth_token" json:"-"`
	TimeoutSeconds int    `yaml:"timeout_seconds" json:"-"`
}

type Config struct {
	Listen        string          `yaml:"listen"`
	AdminUser     string          `yaml:"admin_user"`
	AdminPassword string          `yaml:"admin_password"`
	LanguageDB    string          `yaml:"language_db"`
	LanguageSetID int64           `yaml:"language_set_id"`
	SDK           Peer            `yaml:"sdk_inner"`
	BOOI          map[string]Peer `yaml:"booi_inner"`
	BaseDir       string          `yaml:"-"`
}

type Server struct {
	cfg       Config
	language  *sql.DB
	clientsMu sync.Mutex
	clients   map[string]*http.Client
}

func main() {
	configPath := flag.String("config", "config.yaml", "path to config.yaml")
	flag.Parse()
	cfg, err := loadConfig(*configPath)
	if err != nil {
		log.Fatal(err)
	}
	language, err := openLanguage(filepath.Join(cfg.BaseDir, cfg.LanguageDB))
	if err != nil {
		log.Fatal(err)
	}
	defer language.Close()
	server := &Server{cfg: *cfg, language: language, clients: map[string]*http.Client{}}
	log.Printf("Pape-MUIP listening on http://%s", cfg.Listen)
	log.Fatal((&http.Server{Addr: cfg.Listen, Handler: server.router(), ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout: 30 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: 90 * time.Second}).ListenAndServe())
}

func loadConfig(path string) (*Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var cfg Config
	decoder := yaml.NewDecoder(strings.NewReader(string(raw)))
	decoder.KnownFields(true)
	if err := decoder.Decode(&cfg); err != nil {
		return nil, err
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	cfg.BaseDir = filepath.Dir(abs)
	if cfg.Listen == "" {
		cfg.Listen = "127.0.0.1:18080"
	}
	if cfg.AdminUser == "" || cfg.AdminPassword == "" {
		return nil, errors.New("admin_user and admin_password are required")
	}
	if cfg.LanguageDB == "" {
		cfg.LanguageDB = "languages.sqlite"
	}
	if cfg.LanguageSetID == 0 {
		cfg.LanguageSetID = 1000000000001
	}
	if err := validatePeer("sdk_inner", cfg.SDK); err != nil {
		return nil, err
	}
	if len(cfg.BOOI) == 0 {
		return nil, errors.New("booi_inner requires at least one server")
	}
	for name, peer := range cfg.BOOI {
		if err := validatePeer("booi_inner."+name, peer); err != nil {
			return nil, err
		}
	}
	return &cfg, nil
}

func validatePeer(name string, peer Peer) error {
	parsed, err := url.Parse(peer.BaseURL)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return fmt.Errorf("%s.base_url must be an absolute HTTP(S) URL", name)
	}
	if strings.TrimSpace(peer.AuthToken) == "" {
		return fmt.Errorf("%s.auth_token is required", name)
	}
	return nil
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

func (s *Server) router() *gin.Engine {
	gin.SetMode(gin.ReleaseMode)
	router := gin.New()
	router.Use(gin.Logger(), gin.Recovery(), s.securityHeaders(), gin.BasicAuthForRealm(gin.Accounts{
		s.cfg.AdminUser: s.cfg.AdminPassword,
	}, "Pape MUIP"))
	router.GET("/api/health", s.health)
	router.Any("/api/sdk/*path", s.sdkProxy)
	router.Any("/api/booi/:server/*path", s.booiProxy)
	router.POST("/api/operations/booi/:server/players/:id/full-catalog", s.fullCatalogPlayer)
	router.POST("/api/operations/booi/:server/players/:id/import-sync", s.importSyncPlayer)
	router.GET("/api/language/:id", s.languageLookup)
	content, err := fs.Sub(webFiles, "web")
	if err != nil {
		panic(fmt.Sprintf("load embedded web files: %v", err))
	}
	router.NoRoute(gin.WrapH(http.FileServer(http.FS(content))))
	return router
}

func (s *Server) securityHeaders() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("X-Content-Type-Options", "nosniff")
		c.Header("X-Frame-Options", "DENY")
		c.Header("Referrer-Policy", "no-referrer")
		c.Header("Content-Security-Policy", "default-src 'self'; style-src 'self'; script-src 'self'; connect-src 'self'")
		c.Next()
	}
}

func (s *Server) health(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"ok": true, "service": "pape-muip", "booi_servers": mapKeys(s.cfg.BOOI)})
}

func (s *Server) sdkProxy(c *gin.Context) {
	path := strings.TrimPrefix(c.Param("path"), "/")
	s.proxy(c, "sdk", s.cfg.SDK, "/inner/v1/admin/"+path, false)
}

func (s *Server) booiProxy(c *gin.Context) {
	serverID := c.Param("server")
	peer, ok := s.cfg.BOOI[serverID]
	if !ok {
		c.JSON(http.StatusNotFound, gin.H{"error": "unknown BOOI server"})
		return
	}
	path := strings.TrimPrefix(c.Param("path"), "/")
	s.proxy(c, "booi:"+serverID, peer, "/inner/v1/admin/"+path, strings.HasPrefix(path, "catalog/"))
}

func (s *Server) languageLookup(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid text id"})
		return
	}
	text, err := s.localized(id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"text_id": id, "text": text})
}

func (s *Server) proxy(c *gin.Context, key string, peer Peer, path string, localize bool) {
	base, err := url.Parse(strings.TrimRight(peer.BaseURL, "/"))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	base.Path = path
	base.RawQuery = c.Request.URL.RawQuery
	var body io.Reader
	if c.Request.Body != nil {
		body = io.LimitReader(c.Request.Body, 4<<20)
	}
	req, err := http.NewRequestWithContext(c.Request.Context(), c.Request.Method, base.String(), body)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	req.Header.Set("Authorization", "Bearer "+peer.AuthToken)
	if ct := c.GetHeader("Content-Type"); ct != "" {
		req.Header.Set("Content-Type", ct)
	}
	s.clientsMu.Lock()
	client := s.clients[key]
	if client == nil {
		timeout := peer.TimeoutSeconds
		if timeout <= 0 {
			timeout = 10
		}
		client = &http.Client{Timeout: time.Duration(timeout) * time.Second}
		s.clients[key] = client
	}
	s.clientsMu.Unlock()
	resp, err := client.Do(req)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}
	if localize && resp.StatusCode < 300 {
		data = s.localizeCatalog(data)
	}
	for _, name := range []string{"Content-Type"} {
		if value := resp.Header.Get(name); value != "" {
			c.Header(name, value)
		}
	}
	c.Status(resp.StatusCode)
	_, _ = c.Writer.Write(data)
}

func (s *Server) localized(id int64) (string, error) {
	var text string
	err := s.language.QueryRow("select text from localized_text where resource_set_id=? and text_id=?", s.cfg.LanguageSetID, id).Scan(&text)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return text, err
}

func (s *Server) localizeCatalog(data []byte) []byte {
	var payload map[string]any
	if json.Unmarshal(data, &payload) != nil {
		return data
	}
	rows, ok := payload["rows"].([]any)
	if !ok {
		return data
	}
	ids := []int64{}
	seen := map[int64]bool{}
	for _, raw := range rows {
		row, _ := raw.(map[string]any)
		id := number(row["name_text_id"])
		if id > 0 && !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	names := map[int64]string{}
	if len(ids) > 0 {
		placeholders := strings.TrimRight(strings.Repeat("?,", len(ids)), ",")
		args := make([]any, 0, len(ids)+1)
		args = append(args, s.cfg.LanguageSetID)
		for _, id := range ids {
			args = append(args, id)
		}
		query := "select text_id,text from localized_text where resource_set_id=? and text_id in (" + placeholders + ")"
		if result, err := s.language.Query(query, args...); err == nil {
			for result.Next() {
				var id int64
				var text string
				if result.Scan(&id, &text) == nil {
					names[id] = text
				}
			}
			result.Close()
		}
	}
	for _, raw := range rows {
		row, _ := raw.(map[string]any)
		id := number(row["name_text_id"])
		if names[id] != "" {
			row["localized_name"] = names[id]
		}
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return data
	}
	return encoded
}

func number(v any) int64 {
	switch n := v.(type) {
	case float64:
		return int64(n)
	case json.Number:
		i, _ := n.Int64()
		return i
	case string:
		i, _ := strconv.ParseInt(n, 10, 64)
		return i
	}
	return 0
}
func mapKeys(values map[string]Peer) []string {
	out := make([]string, 0, len(values))
	for key := range values {
		out = append(out, key)
	}
	sort.Strings(out)
	return out
}
