//go:build integration || e2e

package server

import (
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/lbe/sfpg-go/internal/getopt"
)

// TestConfigPersistence_AfterRestart_SiteNamePersist verifies that a config
// value saved through the HTTP handler survives a full app shutdown/restart
// when no CLI/env overrides are present.
func TestConfigPersistence_AfterRestart_SiteNamePersist(t *testing.T) {
	setenvForTest(t, "SEPG_SESSION_SECURE", "false")

	app1 := CreateApp(t)
	defer func() {
		if app1 != nil {
			app1.Shutdown()
		}
	}()

	ts := httptest.NewServer(app1.getRouter())
	defer ts.Close()

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("failed to create cookie jar: %v", err)
	}
	client := &http.Client{Jar: jar}

	loginAsAdmin(t, client, ts.URL)

	csrfToken := extractCSRFTokenFromConfig(t, client, ts.URL)
	formData := url.Values{}
	formData.Set("csrf_token", csrfToken)
	formData.Set("site_name", "Persistence Test")

	req, err := http.NewRequest(http.MethodPost, ts.URL+"/config", strings.NewReader(formData.Encode()))
	if err != nil {
		t.Fatalf("failed to create POST request: %v", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", ts.URL)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("POST /config failed: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	cpcRo, err := app1.dbRoPool.Get()
	if err != nil {
		t.Fatalf("failed to get DB connection: %v", err)
	}

	siteName, err := cpcRo.Queries.GetConfigValueByKey(app1.ctx, "site_name")
	if err != nil {
		app1.dbRoPool.Put(cpcRo)
		t.Fatalf("failed to get site_name from DB: %v", err)
	}
	if siteName != "Persistence Test" {
		app1.dbRoPool.Put(cpcRo)
		t.Errorf("expected site_name='Persistence Test' in DB, got %q", siteName)
	}
	app1.dbRoPool.Put(cpcRo)

	dbPaths := app1.dbPaths
	rootDir := app1.rootDir
	app1.Shutdown()
	app1 = nil

	app2 := New(getopt.Opt{
		SessionSecret: getopt.OptString{String: "this-is-a-test-secret", IsSet: true},
	}, "x.y.z")
	app2.dbPaths = dbPaths
	app2.setRootDir(&rootDir)
	defer app2.Shutdown()

	app2.setDB()
	app2.setConfigDefaults()
	if err := app2.loadConfig(); err != nil {
		t.Fatalf("failed to load config in second app: %v", err)
	}
	app2.ApplyConfig()

	if app2.config.SiteName != "Persistence Test" {
		t.Errorf("expected app2.config.SiteName='Persistence Test', got %q", app2.config.SiteName)
	}
}
