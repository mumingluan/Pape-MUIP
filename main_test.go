package main

import (
	"database/sql"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCatalogProxyAddsLocalizedNameAndUsesInnerAuth(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/inner/v1/admin/catalog/cards" || r.Header.Get("Authorization") != "Bearer inner-secret" {
			t.Fatalf("unexpected upstream request: %s auth=%q", r.URL.Path, r.Header.Get("Authorization"))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"rows":[{"id":"111101","name_text_id":373240,"raw":{}}]}`))
	}))
	defer upstream.Close()

	dbPath := filepath.Join(t.TempDir(), "languages.sqlite")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`create table localized_text(resource_set_id integer, text_id integer, text text, package_id integer,
		primary key(resource_set_id,text_id)); insert into localized_text values(1000000000001,373240,'比心',1)`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	language, err := openLanguage(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer language.Close()
	s := &Server{cfg: Config{LanguageSetID: 1000000000001, BOOI: map[string]Peer{"500058": {BaseURL: upstream.URL, AuthToken: "inner-secret"}}}, language: language, clients: map[string]*http.Client{}}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/booi/500058/catalog/cards", nil)
	s.api(recorder, request)
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"localized_name":"比心"`) {
		t.Fatalf("response=%d %s", recorder.Code, recorder.Body.String())
	}
}

func TestBasicAuthenticationProtectsUIAndAPI(t *testing.T) {
	s := &Server{cfg: Config{AdminUser: "admin", AdminPassword: "secret"}}
	handler := s.authenticate(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	for _, test := range []struct {
		user, password string
		want           int
	}{{want: 401}, {user: "admin", password: "wrong", want: 401}, {user: "admin", password: "secret", want: 204}} {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		if test.user != "" {
			req.SetBasicAuth(test.user, test.password)
		}
		res := httptest.NewRecorder()
		handler.ServeHTTP(res, req)
		if res.Code != test.want {
			t.Fatalf("auth %q status=%d want=%d", test.user, res.Code, test.want)
		}
	}
}

func TestLoadConfigRejectsUnprotectedInnerPeer(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	raw := `admin_user: admin
admin_password: secret
sdk_inner:
  base_url: http://127.0.0.1:18081
  auth_token: ""
booi_inner:
  "500058":
    base_url: http://127.0.0.1:18082
    auth_token: protected
`
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadConfig(path); err == nil || !strings.Contains(err.Error(), "sdk_inner.auth_token") {
		t.Fatalf("unexpected validation result: %v", err)
	}
}
