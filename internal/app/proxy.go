package app

import (
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"pape-muip/internal/config"
)

func (s *App) health(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"ok": true, "service": "pape-muip", "booi_servers": mapKeys(s.cfg.BOOI)})
}

func (s *App) sdkProxy(c *gin.Context) {
	path := strings.TrimPrefix(c.Param("path"), "/")
	var transform func([]byte) []byte
	if path == "reports" {
		transform = s.localizeReports
	}
	s.proxy(c, "sdk", s.cfg.SDK, "/inner/v1/admin/"+path, transform)
}

func (s *App) booiProxy(c *gin.Context) {
	serverID := c.Param("server")
	peer, ok := s.cfg.BOOI[serverID]
	if !ok {
		c.JSON(http.StatusNotFound, gin.H{"error": "unknown BOOI server"})
		return
	}
	path := strings.TrimPrefix(c.Param("path"), "/")
	var transform func([]byte) []byte
	if strings.HasPrefix(path, "catalog/") {
		transform = s.localizeCatalog
	}
	s.proxy(c, "booi:"+serverID, peer, "/inner/v1/admin/"+path, transform)
}

func (s *App) proxy(c *gin.Context, key string, peer config.Peer, path string, transform func([]byte) []byte) {
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
	if transform != nil && resp.StatusCode < 300 {
		data = transform(data)
	}
	if value := resp.Header.Get("Content-Type"); value != "" {
		c.Header("Content-Type", value)
	}
	c.Status(resp.StatusCode)
	_, _ = c.Writer.Write(data)
}

func mapKeys(values map[string]config.Peer) []string {
	out := make([]string, 0, len(values))
	for key := range values {
		out = append(out, key)
	}
	sort.Strings(out)
	return out
}
