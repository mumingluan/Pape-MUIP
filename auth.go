package main

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

const (
	sessionCookieName = "pape_muip_session"
	sessionLifetime   = 12 * time.Hour
)

func (s *Server) loginPage(c *gin.Context) {
	if token, err := c.Cookie(sessionCookieName); err == nil && s.validSession(token) {
		c.Redirect(http.StatusSeeOther, "/")
		return
	}
	s.renderLogin(c, http.StatusOK, "")
}

func (s *Server) login(c *gin.Context) {
	username := c.PostForm("username")
	password := c.PostForm("password")
	wantUser := []byte(s.cfg.AdminUser)
	wantPassword := []byte(s.cfg.AdminPassword)
	valid := len(username) == len(wantUser) && len(password) == len(wantPassword) &&
		subtle.ConstantTimeCompare([]byte(username), wantUser) == 1 &&
		subtle.ConstantTimeCompare([]byte(password), wantPassword) == 1
	if !valid {
		s.renderLogin(c, http.StatusUnauthorized, "用户名或密码错误")
		return
	}
	token, expires, err := s.newSession()
	if err != nil {
		c.String(http.StatusInternalServerError, "无法创建登录会话")
		return
	}
	http.SetCookie(c.Writer, &http.Cookie{
		Name: sessionCookieName, Value: token, Path: "/", Expires: expires,
		MaxAge: int(sessionLifetime.Seconds()), HttpOnly: true, Secure: c.Request.TLS != nil,
		SameSite: http.SameSiteStrictMode,
	})
	c.Redirect(http.StatusSeeOther, "/")
}

func (s *Server) logout(c *gin.Context) {
	if token, err := c.Cookie(sessionCookieName); err == nil {
		s.sessionsMu.Lock()
		delete(s.sessions, token)
		s.sessionsMu.Unlock()
	}
	s.clearSessionCookie(c)
	c.Redirect(http.StatusSeeOther, "/login")
}

func (s *Server) requireSession() gin.HandlerFunc {
	return func(c *gin.Context) {
		token, err := c.Cookie(sessionCookieName)
		if err == nil && s.validSession(token) {
			c.Next()
			return
		}
		s.clearSessionCookie(c)
		if strings.HasPrefix(c.Request.URL.Path, "/api/") {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "login required"})
			return
		}
		c.Redirect(http.StatusSeeOther, "/login")
		c.Abort()
	}
}

func (s *Server) newSession() (string, time.Time, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", time.Time{}, err
	}
	token := base64.RawURLEncoding.EncodeToString(raw)
	now := time.Now()
	expires := now.Add(sessionLifetime)
	s.sessionsMu.Lock()
	for existing, deadline := range s.sessions {
		if !deadline.After(now) {
			delete(s.sessions, existing)
		}
	}
	s.sessions[token] = expires
	s.sessionsMu.Unlock()
	return token, expires, nil
}

func (s *Server) validSession(token string) bool {
	if token == "" {
		return false
	}
	now := time.Now()
	s.sessionsMu.Lock()
	expires, ok := s.sessions[token]
	if ok && !expires.After(now) {
		delete(s.sessions, token)
		ok = false
	}
	s.sessionsMu.Unlock()
	return ok
}

func (s *Server) clearSessionCookie(c *gin.Context) {
	http.SetCookie(c.Writer, &http.Cookie{
		Name: sessionCookieName, Value: "", Path: "/", MaxAge: -1, Expires: time.Unix(1, 0),
		HttpOnly: true, Secure: c.Request.TLS != nil, SameSite: http.SameSiteStrictMode,
	})
}

func (s *Server) renderLogin(c *gin.Context, status int, message string) {
	c.Header("Content-Type", "text/html; charset=utf-8")
	c.Header("Cache-Control", "no-store")
	c.Status(status)
	if err := s.loginTemplate.ExecuteTemplate(c.Writer, "login.html", struct{ Error string }{Error: message}); err != nil {
		_ = c.Error(err)
	}
}
