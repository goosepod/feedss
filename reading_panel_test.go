package main

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadingPanelSettings(t *testing.T) {
	app, err := NewApp(AppConfig{DBPath: filepath.Join(t.TempDir(), "test.db"), Port: defaultPort})
	if err != nil {
		t.Fatal(err)
	}
	defer app.db.Close()
	user, err := app.createUser("admin", "pw123", true)
	if err != nil {
		t.Fatal(err)
	}
	settings, err := app.getSettings()
	if err != nil {
		t.Fatal(err)
	}
	if settings.LinkOpenTarget != "tab" || settings.ReadingPanelLayout != "right" {
		t.Fatalf("unexpected defaults: %#v", settings)
	}
	for _, tc := range []struct {
		target, layout string
		status         int
	}{
		{"panel", "right", 200}, {"panel", "bottom", 200}, {"invalid", "bottom", 400}, {"panel", "invalid", 400}, {"tab", "right", 200},
	} {
		form := url.Values{"link_open_target": {tc.target}, "reading_panel_layout": {tc.layout}}
		request := httptest.NewRequest(http.MethodPost, "/api/settings", strings.NewReader(form.Encode()))
		request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		addSessionCookie(t, app, request, user)
		response := httptest.NewRecorder()
		app.handleSettingsAPI(response, request)
		if response.Code != tc.status {
			t.Fatalf("%+v: %d %s", tc, response.Code, response.Body.String())
		}
		got, err := app.getSettings()
		if err != nil {
			t.Fatal(err)
		}
		if tc.status == 200 {
			settings = got
			if got.LinkOpenTarget != tc.target || got.ReadingPanelLayout != tc.layout {
				t.Fatalf("settings not persisted: %#v", got)
			}
		} else if got.LinkOpenTarget != settings.LinkOpenTarget || got.ReadingPanelLayout != settings.ReadingPanelLayout {
			t.Fatal("invalid settings changed stored preferences")
		}
	}
	// Reapplying schema initialization must preserve the saved selection.
	if err := initSchema(app.db); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	addSessionCookie(t, app, request, user)
	response := httptest.NewRecorder()
	app.handleIndex(response, request)
	if !strings.Contains(response.Body.String(), `id="app-version" class="dialog-subtitle">feedss `+version+`</p>`) {
		t.Fatal("settings do not show the running build version")
	}
}
