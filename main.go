package main

import (
	"crypto/subtle"
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
	mux := http.NewServeMux()
	mux.HandleFunc("/api/", server.api)
	content, _ := fs.Sub(webFiles, "web")
	mux.Handle("/", http.FileServer(http.FS(content)))
	handler := server.authenticate(server.securityHeaders(mux))
	log.Printf("Pape-MUIP listening on http://%s", cfg.Listen)
	log.Fatal((&http.Server{Addr: cfg.Listen, Handler: handler, ReadHeaderTimeout: 10 * time.Second,
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

func (s *Server) authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, password, ok := r.BasicAuth()
		wantUser, wantPassword := []byte(s.cfg.AdminUser), []byte(s.cfg.AdminPassword)
		if !ok || len(user) != len(wantUser) || len(password) != len(wantPassword) || subtle.ConstantTimeCompare([]byte(user), wantUser) != 1 || subtle.ConstantTimeCompare([]byte(password), wantPassword) != 1 {
			w.Header().Set("WWW-Authenticate", `Basic realm="Pape MUIP"`)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; style-src 'self'; script-src 'self'; connect-src 'self'")
		next.ServeHTTP(w, r)
	})
}

func (s *Server) api(w http.ResponseWriter, r *http.Request) {
	path := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/"), "/")
	parts := strings.Split(path, "/")
	if path == "health" {
		s.json(w, 200, map[string]any{"ok": true, "service": "pape-muip", "booi_servers": mapKeys(s.cfg.BOOI)})
		return
	}
	if len(parts) >= 1 && parts[0] == "sdk" {
		s.proxy(w, r, "sdk", s.cfg.SDK, "/inner/v1/admin/"+strings.Join(parts[1:], "/"), false)
		return
	}
	if len(parts) >= 3 && parts[0] == "booi" {
		peer, ok := s.cfg.BOOI[parts[1]]
		if !ok {
			s.json(w, 404, map[string]any{"error": "unknown BOOI server"})
			return
		}
		upstream := "/inner/v1/admin/" + strings.Join(parts[2:], "/")
		s.proxy(w, r, "booi:"+parts[1], peer, upstream, len(parts) >= 4 && parts[2] == "catalog")
		return
	}
	if len(parts) == 2 && parts[0] == "language" {
		id, err := strconv.ParseInt(parts[1], 10, 64)
		if err != nil {
			s.json(w, 400, map[string]any{"error": "invalid text id"})
			return
		}
		text, _ := s.localized(id)
		s.json(w, 200, map[string]any{"text_id": id, "text": text})
		return
	}
	s.json(w, 404, map[string]any{"error": "not found"})
}

func (s *Server) proxy(w http.ResponseWriter, r *http.Request, key string, peer Peer, path string, localize bool) {
	base, err := url.Parse(strings.TrimRight(peer.BaseURL, "/"))
	if err != nil {
		s.json(w, 500, map[string]any{"error": err.Error()})
		return
	}
	base.Path = path
	base.RawQuery = r.URL.RawQuery
	var body io.Reader
	if r.Body != nil {
		body = io.LimitReader(r.Body, 4<<20)
	}
	req, err := http.NewRequestWithContext(r.Context(), r.Method, base.String(), body)
	if err != nil {
		s.json(w, 500, map[string]any{"error": err.Error()})
		return
	}
	req.Header.Set("Authorization", "Bearer "+peer.AuthToken)
	if ct := r.Header.Get("Content-Type"); ct != "" {
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
		s.json(w, 502, map[string]any{"error": err.Error()})
		return
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if err != nil {
		s.json(w, 502, map[string]any{"error": err.Error()})
		return
	}
	if localize && resp.StatusCode < 300 {
		data = s.localizeCatalog(data)
	}
	for _, name := range []string{"Content-Type"} {
		if value := resp.Header.Get(name); value != "" {
			w.Header().Set(name, value)
		}
	}
	w.WriteHeader(resp.StatusCode)
	_, _ = w.Write(data)
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
func (s *Server) json(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
