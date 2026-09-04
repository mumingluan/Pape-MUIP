package app

import (
	"compress/gzip"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

type gzipResponseWriter struct {
	gin.ResponseWriter
	writer *gzip.Writer
}

func (w *gzipResponseWriter) Write(data []byte) (int, error) {
	w.Header().Del("Content-Length")
	return w.writer.Write(data)
}

func (w *gzipResponseWriter) WriteString(data string) (int, error) {
	w.Header().Del("Content-Length")
	return w.writer.Write([]byte(data))
}

func (w *gzipResponseWriter) WriteHeader(statusCode int) {
	w.Header().Del("Content-Length")
	w.ResponseWriter.WriteHeader(statusCode)
}

func (w *gzipResponseWriter) Flush() {
	_ = w.writer.Flush()
	w.ResponseWriter.Flush()
}

func gzipResponses() gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.Request.Method == http.MethodHead || !strings.Contains(c.GetHeader("Accept-Encoding"), "gzip") {
			c.Next()
			return
		}
		c.Header("Content-Encoding", "gzip")
		c.Header("Vary", "Accept-Encoding")
		writer := gzip.NewWriter(c.Writer)
		c.Writer = &gzipResponseWriter{ResponseWriter: c.Writer, writer: writer}
		c.Next()
		_ = writer.Close()
	}
}

func (s *App) securityHeaders() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("X-Content-Type-Options", "nosniff")
		c.Header("X-Frame-Options", "DENY")
		c.Header("Referrer-Policy", "no-referrer")
		c.Header("Content-Security-Policy", "default-src 'self'; style-src 'self'; script-src 'self'; connect-src 'self'")
		c.Next()
	}
}
