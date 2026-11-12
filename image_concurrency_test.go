package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
)

func TestConcurrentImageSessionRenewals(t *testing.T) {
	app, err := NewApp(AppConfig{DBPath: filepath.Join(t.TempDir(), "images.db"), Port: defaultPort})
	if err != nil {
		t.Fatal(err)
	}
	defer app.db.Close()
	user, err := app.createUser("reader", "pw123", false)
	if err != nil {
		t.Fatal(err)
	}
	initial := httptest.NewRequest(http.MethodGet, "/", nil)
	addSessionCookie(t, app, initial, user)
	cookie, _ := initial.Cookie("feedss_user")
	handler := app.requireAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	start := make(chan struct{})
	results := make(chan string, 32)
	for i := 0; i < 32; i++ {
		go func() {
			<-start
			request := httptest.NewRequest(http.MethodGet, "/api/image?url=https://example.com/comic.png", nil)
			request.AddCookie(cookie)
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != http.StatusNoContent {
				results <- fmt.Sprintf("%d %s", response.Code, response.Body.String())
			} else {
				results <- ""
			}
		}()
	}
	close(start)
	for i := 0; i < 32; i++ {
		if failure := <-results; failure != "" {
			t.Error(failure)
		}
	}
}
