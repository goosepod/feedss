package main

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
)

func TestDiscoverArticleImage(t *testing.T) {
	page, _ := url.Parse("https://cad-comic.com/comic/title/")
	tests := []struct{ name, html, want string }{
		{"actual strip before cropped social card", `<meta property="og:image" content="/cropped.webp"><img src="/logo.png"><div class="comicpage"><a><img src="/comic-image/123/?token=public&amp;expires=42"></a></div>`, "https://cad-comic.com/comic-image/123/?token=public&expires=42"},
		{"penny arcade full strip", `<meta property="og:image" content="https://assets.penny-arcade.com/comics/full.jpg"><img src="/panels/one.jpg">`, "https://assets.penny-arcade.com/comics/full.jpg"},
		{"smbc strip", `<img id="comicimage" src="//www.smbc-comics.com/comics/today.png">`, "https://www.smbc-comics.com/comics/today.png"},
		{"lazy comic", `<div id="comic"><img src="/placeholder.gif" data-src="../../strip.png"></div>`, "https://cad-comic.com/strip.png"},
		{"semi coop strip", `<div class="webcomic-image"><a><img src="/uploads/strip.jpg"></a></div>`, "https://cad-comic.com/uploads/strip.jpg"},
		{"generic social image", `<meta property="og:image" content="/news/lead.jpg">`, "https://cad-comic.com/news/lead.jpg"},
		{"generic twitter image", `<meta name="twitter:image" content="/lead.jpg">`, "https://cad-comic.com/lead.jpg"},
		{"generic article image", `<header><img src="/logo.png"></header><main><article><img src="/article.jpg"></article></main>`, "https://cad-comic.com/article.jpg"},
		{"no arbitrary image", `<img src="/logo.png">`, ""},
		{"unsafe image", `<meta property="og:image" content="javascript:alert(1)">`, ""},
		{"unsafe strip falls back", `<img id="comicimage" src="data:image/png;base64,x"><meta property="og:image" content="/strip.png">`, "https://cad-comic.com/strip.png"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := discoverArticleImage(page, strings.NewReader(tt.html)); got != tt.want {
				t.Fatalf("got %q want %q", got, tt.want)
			}
		})
	}
}

func TestArticleImageRequiresOwnedArticle(t *testing.T) {
	app, err := NewApp(AppConfig{DBPath: filepath.Join(t.TempDir(), "comic.db"), Port: defaultPort})
	if err != nil {
		t.Fatal(err)
	}
	defer app.db.Close()
	user, err := app.createUser("reader", "pw123", false)
	if err != nil {
		t.Fatal(err)
	}
	other, err := app.createUser("other", "pw123", false)
	if err != nil {
		t.Fatal(err)
	}
	groupID := app.ensureGroup(other.ID, "comics")
	result, err := app.db.Exec("INSERT INTO feeds(title, url, group_id, created_by) VALUES('comic', 'https://cad-comic.com/feed/', ?, ?)", groupID, other.ID)
	if err != nil {
		t.Fatal(err)
	}
	feedID, _ := result.LastInsertId()
	_, err = app.db.Exec("INSERT INTO articles(id,feed_id,title,link,guid) VALUES(123,?,'Comic','https://cad-comic.com/comic/title/','comic')", feedID)
	if err != nil {
		t.Fatal(err)
	}
	for _, signedIn := range []bool{false, true} {
		request := httptest.NewRequest(http.MethodGet, "/api/article-image?id=123", nil)
		if signedIn {
			addSessionCookie(t, app, request, user)
		}
		response := httptest.NewRecorder()
		app.handleArticleImage(response, request)
		want := http.StatusUnauthorized
		if signedIn {
			want = http.StatusNotFound
		}
		if response.Code != want {
			t.Fatalf("got %d want %d", response.Code, want)
		}
	}
}
