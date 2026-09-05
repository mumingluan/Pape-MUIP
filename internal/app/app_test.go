package app

import (
	"compress/gzip"
	"database/sql"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"pape-muip/internal/config"
)

func TestCatalogProxyAddsLocalizedNameAndUsesInnerAuth(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/inner/v1/admin/catalog/cards" || r.Header.Get("Authorization") != "Bearer inner-secret" {
			t.Fatalf("unexpected upstream request: %s auth=%q", r.URL.Path, r.Header.Get("Authorization"))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"rows":[{"id":"111101","name_text_id":373240,"raw":{}},{"id":"3","fallback_name":"体力","raw":{}}]}`))
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
	s := &App{cfg: config.Config{AdminUser: "admin", AdminPassword: "secret",
		SDK:  config.Peer{BaseURL: upstream.URL, AuthToken: "sdk-secret"},
		BOOI: map[string]config.Peer{"500058": {BaseURL: upstream.URL, AuthToken: "inner-secret"}}}, language: language, clients: map[string]*http.Client{}}
	handler := s.router()
	cookie := loginCookie(t, handler)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/booi/500058/catalog/cards", nil)
	request.AddCookie(cookie)
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"localized_name":"比心"`) || !strings.Contains(recorder.Body.String(), `"localized_name":"体力"`) {
		t.Fatalf("response=%d %s", recorder.Code, recorder.Body.String())
	}
}

func TestReportProxyAddsLocalizedMetadata(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/inner/v1/admin/reports" || r.Header.Get("Authorization") != "Bearer sdk-secret" {
			t.Fatalf("unexpected upstream request: %s auth=%q", r.URL.Path, r.Header.Get("Authorization"))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"total":1,"reports":[{"reason_id":7,"source":101,"platform":2}]}`))
	}))
	defer upstream.Close()

	dbPath := filepath.Join(t.TempDir(), "languages.sqlite")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`create table localized_text(resource_set_id integer, text_id integer, text text, package_id integer,
		primary key(resource_set_id,text_id)); insert into localized_text values(1000000000001,567404,'个人信息违规',1)`); err != nil {
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
	s := &App{cfg: config.Config{AdminUser: "admin", AdminPassword: "secret",
		SDK: config.Peer{BaseURL: upstream.URL, AuthToken: "sdk-secret"}}, language: language, clients: map[string]*http.Client{}}
	handler := s.router()
	cookie := loginCookie(t, handler)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/sdk/reports?limit=50", nil)
	request.AddCookie(cookie)
	handler.ServeHTTP(recorder, request)
	body := recorder.Body.String()
	for _, expected := range []string{`"reason_name":"个人信息违规"`, `"source_name":"玩家资料页"`, `"platform_name":"Android"`} {
		if recorder.Code != http.StatusOK || !strings.Contains(body, expected) {
			t.Fatalf("localized report missing %s: response=%d %s", expected, recorder.Code, body)
		}
	}
}

func TestHTMLLoginSessionProtectsUIAndAPI(t *testing.T) {
	s := &App{cfg: config.Config{AdminUser: "admin", AdminPassword: "secret", BOOI: map[string]config.Peer{}}}
	handler := s.router()

	apiRes := httptest.NewRecorder()
	handler.ServeHTTP(apiRes, httptest.NewRequest(http.MethodGet, "/api/health", nil))
	if apiRes.Code != http.StatusUnauthorized || !strings.Contains(apiRes.Body.String(), "login required") {
		t.Fatalf("unauthenticated API response=%d %q", apiRes.Code, apiRes.Body.String())
	}
	if apiRes.Header().Get("WWW-Authenticate") != "" {
		t.Fatal("Basic Auth challenge must not be emitted")
	}
	styleRes := httptest.NewRecorder()
	handler.ServeHTTP(styleRes, httptest.NewRequest(http.MethodGet, "/style.css", nil))
	if styleRes.Code != http.StatusOK || !strings.Contains(styleRes.Body.String(), "prefers-color-scheme") {
		t.Fatalf("public login stylesheet response=%d", styleRes.Code)
	}
	pageRes := httptest.NewRecorder()
	handler.ServeHTTP(pageRes, httptest.NewRequest(http.MethodGet, "/", nil))
	if pageRes.Code != http.StatusSeeOther || pageRes.Header().Get("Location") != "/login" {
		t.Fatalf("unauthenticated page response=%d location=%q", pageRes.Code, pageRes.Header().Get("Location"))
	}
	badForm := url.Values{"username": {"admin"}, "password": {"wrong"}}
	badReq := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(badForm.Encode()))
	badReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	badRes := httptest.NewRecorder()
	handler.ServeHTTP(badRes, badReq)
	if badRes.Code != http.StatusUnauthorized || !strings.Contains(badRes.Body.String(), "用户名或密码错误") {
		t.Fatalf("bad login response=%d %q", badRes.Code, badRes.Body.String())
	}

	cookie := loginCookie(t, handler)
	req := httptest.NewRequest(http.MethodGet, "/api/health", nil)
	req.AddCookie(cookie)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("authenticated API response=%d %q", res.Code, res.Body.String())
	}
	if res.Header().Get("X-Frame-Options") != "DENY" {
		t.Fatal("security headers are missing")
	}

	logoutReq := httptest.NewRequest(http.MethodPost, "/logout", nil)
	logoutReq.AddCookie(cookie)
	logoutRes := httptest.NewRecorder()
	handler.ServeHTTP(logoutRes, logoutReq)
	if logoutRes.Code != http.StatusSeeOther || logoutRes.Header().Get("Location") != "/login" {
		t.Fatalf("logout response=%d location=%q", logoutRes.Code, logoutRes.Header().Get("Location"))
	}
	expiredReq := httptest.NewRequest(http.MethodGet, "/api/health", nil)
	expiredReq.AddCookie(cookie)
	expiredRes := httptest.NewRecorder()
	handler.ServeHTTP(expiredRes, expiredReq)
	if expiredRes.Code != http.StatusUnauthorized {
		t.Fatalf("logged-out session remained valid: %d", expiredRes.Code)
	}
}

func TestGinRouterCompressesResponses(t *testing.T) {
	s := &App{cfg: config.Config{AdminUser: "admin", AdminPassword: "secret", BOOI: map[string]config.Peer{}}}
	handler := s.router()
	cookie := loginCookie(t, handler)
	req := httptest.NewRequest(http.MethodGet, "/app.js", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	req.AddCookie(cookie)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK || res.Header().Get("Content-Encoding") != "gzip" {
		t.Fatalf("compressed response=%d encoding=%q", res.Code, res.Header().Get("Content-Encoding"))
	}
	reader, err := gzip.NewReader(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	if err := reader.Close(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "PAGE_SIZE=100") {
		t.Fatal("compressed script body is invalid")
	}
}

func TestGinRouterServesEmbeddedUI(t *testing.T) {
	s := &App{cfg: config.Config{AdminUser: "admin", AdminPassword: "secret"}}
	handler := s.router()
	cookie := loginCookie(t, handler)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(cookie)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK || !strings.Contains(res.Body.String(), "Pape MUIP") {
		t.Fatalf("embedded UI response=%d %q", res.Code, res.Body.String())
	}
	scriptReq := httptest.NewRequest(http.MethodGet, "/app.js", nil)
	scriptReq.AddCookie(cookie)
	scriptRes := httptest.NewRecorder()
	handler.ServeHTTP(scriptRes, scriptReq)
	for _, marker := range []string{"createSearchSelect", "PAGE_SIZE=100", "pagedRows", "resourceOptions('items'", "resourceOptions('cards'", "resourceOptions('stages'", "resourceOptions('tasks'", "players-sequence", "movePlayer", "hardDeletePlayer", "restoreAccount", "restorePlayer", "可选", "record-badges", "p.deleted_at", "p.banned", "datetime-local", "permanent", "timed", "zh-cn", "zh-tw", "jp", "ko"} {
		if scriptRes.Code != http.StatusOK || !strings.Contains(scriptRes.Body.String(), marker) {
			t.Fatalf("embedded searchable selectors missing %q: response=%d", marker, scriptRes.Code)
		}
	}
	if strings.Contains(scriptRes.Body.String(), "disabled=isDeleted") {
		t.Fatal("soft-deleted players still disable GM controls")
	}
}

func loginCookie(t *testing.T, handler http.Handler) *http.Cookie {
	t.Helper()
	form := url.Values{"username": {"admin"}, "password": {"secret"}}
	req := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusSeeOther || res.Header().Get("Location") != "/" {
		t.Fatalf("login response=%d location=%q body=%q", res.Code, res.Header().Get("Location"), res.Body.String())
	}
	for _, cookie := range res.Result().Cookies() {
		if cookie.Name == sessionCookieName {
			if !cookie.HttpOnly || cookie.SameSite != http.SameSiteStrictMode {
				t.Fatalf("unsafe session cookie: %+v", cookie)
			}
			return cookie
		}
	}
	t.Fatal("login did not set a session cookie")
	return nil
}
