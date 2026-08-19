package main

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mmcdole/gofeed"
)

func addSessionCookie(t *testing.T, app *App, request *http.Request, user *User) {
	t.Helper()
	response := httptest.NewRecorder()
	if err := app.setSession(response, user); err != nil {
		t.Fatal(err)
	}
	cookies := response.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("expected one session cookie, got %d", len(cookies))
	}
	request.AddCookie(cookies[0])
}

func TestSessionsUseOpaqueRevocableTokens(t *testing.T) {
	app, err := NewApp(AppConfig{DBPath: filepath.Join(t.TempDir(), "feedss_test.db"), Port: defaultPort})
	if err != nil {
		t.Fatal(err)
	}
	defer app.db.Close()
	user, err := app.createUser("reader", "pw123", false)
	if err != nil {
		t.Fatal(err)
	}

	request := httptest.NewRequest(http.MethodGet, "/", nil)
	addSessionCookie(t, app, request, user)
	cookie, err := request.Cookie("feedss_user")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(cookie.Value, "reader") || strings.Contains(cookie.Value, strconv.FormatInt(user.ID, 10)+"|") {
		t.Fatalf("session cookie exposes identity data: %q", cookie.Value)
	}
	if session, ok := app.getSession(request); !ok || session.ID != user.ID {
		t.Fatalf("stored session was not accepted: %#v ok=%v", session, ok)
	}
	var storedToken string
	if err := app.db.QueryRow("SELECT token_hash FROM sessions WHERE user_id = ?", user.ID).Scan(&storedToken); err != nil {
		t.Fatal(err)
	}
	if storedToken == cookie.Value {
		t.Fatal("raw session token was stored in SQLite")
	}

	forged := httptest.NewRequest(http.MethodGet, "/", nil)
	forged.AddCookie(&http.Cookie{Name: "feedss_user", Value: strconv.FormatInt(user.ID, 10) + "|reader|0"})
	if _, ok := app.getSession(forged); ok {
		t.Fatal("forged legacy identity cookie was accepted")
	}

	app.clearSession(httptest.NewRecorder(), request)
	if _, ok := app.getSession(request); ok {
		t.Fatal("revoked session was accepted")
	}
}

func TestFetchFeedUsesHTTPValidators(t *testing.T) {
	const etag = `"feed-v1"`
	const modified = "Wed, 19 Aug 2026 12:00:00 GMT"
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.Header.Get("If-None-Match") == etag && r.Header.Get("If-Modified-Since") == modified {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Header().Set("Content-Type", "application/rss+xml")
		w.Header().Set("ETag", etag)
		w.Header().Set("Last-Modified", modified)
		fmt.Fprint(w, `<?xml version="1.0"?><rss version="2.0"><channel><title>Conditional feed</title></channel></rss>`)
	}))
	defer server.Close()

	feed, gotETag, gotModified, notModified, err := fetchFeed(server.URL, "", "")
	if err != nil || notModified || feed.Title != "Conditional feed" {
		t.Fatalf("unexpected first fetch: feed=%#v notModified=%v err=%v", feed, notModified, err)
	}
	if gotETag != etag || gotModified != modified {
		t.Fatalf("validators = %q %q", gotETag, gotModified)
	}
	feed, _, _, notModified, err = fetchFeed(server.URL, gotETag, gotModified)
	if err != nil || !notModified || feed != nil || requests != 2 {
		t.Fatalf("unexpected conditional fetch: feed=%#v requests=%d notModified=%v err=%v", feed, requests, notModified, err)
	}
}

func TestPWAAssetsAreEmbedded(t *testing.T) {
	manifest, err := staticFS.ReadFile("static/manifest.webmanifest")
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{`"display": "standalone"`, `icon-192.png`, `icon-512.png`} {
		if !strings.Contains(string(manifest), expected) {
			t.Fatalf("manifest missing %q", expected)
		}
	}
	worker, err := staticFS.ReadFile("static/service-worker.js")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(worker), "caches.put('/api/") || !strings.Contains(string(worker), "offline.html") {
		t.Fatal("service worker must avoid private API caching and provide an offline shell")
	}
}

func TestAPIModelsUseBrowserFieldNames(t *testing.T) {
	payload, err := json.Marshal(struct {
		Group    FeedGroup
		Feed     Feed
		Article  Article
		Settings AppSettings
	}{
		Group:    FeedGroup{ID: 1, FeedCount: 2},
		Feed:     Feed{ID: 3, GroupID: 1, DisplayMode: "full", SortDirection: "asc"},
		Article:  Article{ID: 4, FeedID: 3, FeedTitle: "Example", CommentsLink: "https://example.com/comments"},
		Settings: AppSettings{RefreshIntervalMin: 15, MaxArticlesPerFeed: 500, AutoRefreshEnabled: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	jsonText := string(payload)
	for _, field := range []string{"\"id\"", "\"feed_count\"", "\"unread_count\"", "\"group_id\"", "\"display_mode\"", "\"last_refresh_error\"", "\"feed_title\"", "\"comments_link\"", "\"is_read\"", "\"refresh_interval_min\"", "\"auto_refresh_enabled\"", "\"release_check_enabled\"", "\"release_check_include_prereleases\""} {
		if !strings.Contains(jsonText, field) {
			t.Fatalf("expected API JSON to contain %s, got %s", field, jsonText)
		}
	}
	if strings.Contains(jsonText, "\"ID\"") || strings.Contains(jsonText, "\"GroupID\"") {
		t.Fatalf("API JSON leaked Go field names: %s", jsonText)
	}
}

func TestStartupBannerIncludesRuntimeInformation(t *testing.T) {
	previousVersion := version
	version = "v1.2.3"
	defer func() { version = previousVersion }()
	banner := startupBanner(AppConfig{DBPath: "/data/feedss.db", Port: "4317"}, true, 15)
	for _, text := range []string{"███████╗", "Version:        v1.2.3", "Database:       /data/feedss.db", "http://0.0.0.0:4317", "enabled, every 15 minutes", "https://github.com/goosepod/feedss", "GPL-3.0"} {
		if !strings.Contains(banner, text) {
			t.Fatalf("startup banner missing %q:\n%s", text, banner)
		}
	}
}

func TestIndexDisablesBrowserCaching(t *testing.T) {
	app, err := NewApp(AppConfig{DBPath: filepath.Join(t.TempDir(), "feedss_test.db"), Port: defaultPort})
	if err != nil {
		t.Fatal(err)
	}
	defer app.db.Close()

	request := httptest.NewRequest(http.MethodGet, "/", nil)
	response := httptest.NewRecorder()
	app.handleIndex(response, request)

	if got := response.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control = %q, want no-store", got)
	}
	if !strings.Contains(response.Body.String(), `id="account-btn"`) {
		t.Fatal("current app shell is missing the account button")
	}
}

func TestReleaseVersionComparison(t *testing.T) {
	for _, test := range []struct {
		left  string
		right string
		want  int
	}{
		{left: "v0.1.1", right: "v0.1.0", want: 1},
		{left: "0.1.0", right: "v0.1.0", want: 0},
		{left: "v0.2.0-beta.1", right: "v0.1.9", want: 1},
		{left: "v0.1.0", right: "dev", want: 0},
		{left: "dev", right: "v0.1.0", want: 0},
	} {
		if got := compareReleaseVersions(test.left, test.right); got != test.want {
			t.Fatalf("compareReleaseVersions(%q, %q) = %d, want %d", test.left, test.right, got, test.want)
		}
	}
}

func TestImageProxyAddressValidation(t *testing.T) {
	for _, address := range []string{"127.0.0.1", "10.0.0.1", "169.254.169.254", "100.64.0.1", "::1"} {
		if !isBlockedImageIP(net.ParseIP(address)) {
			t.Fatalf("expected %s to be blocked", address)
		}
	}
	if isBlockedImageIP(net.ParseIP("1.1.1.1")) {
		t.Fatal("expected public address to be allowed")
	}
	for _, rawURL := range []string{"ftp://example.com/image.png", "http://user:pass@example.com/image.png", "http://127.0.0.1/image.png"} {
		parsed, err := parseRemoteImageURL(rawURL)
		if rawURL == "http://127.0.0.1/image.png" {
			if err != nil || parsed == nil {
				t.Fatalf("literal private address should parse before DNS validation: %v", err)
			}
			continue
		}
		if err == nil {
			t.Fatalf("expected %s to be rejected", rawURL)
		}
	}
}

func TestDiscoverFaviconURL(t *testing.T) {
	siteURL, err := url.Parse("https://example.com/")
	if err != nil {
		t.Fatal(err)
	}
	document := `<html><head><link rel="icon" type="image/png" href="/assets/site-icon.png"></head></html>`
	got := discoverFaviconURL(siteURL, strings.NewReader(document))
	if got != "https://example.com/assets/site-icon.png" {
		t.Fatalf("unexpected favicon URL: %q", got)
	}
}

func TestFaviconCache(t *testing.T) {
	app := &App{}
	app.storeCachedFavicon("https://example.com/", cachedFavicon{
		data: []byte("icon"), contentType: "image/png", expiresAt: time.Now().Add(time.Hour),
	})
	cached, ok := app.loadCachedFavicon("https://example.com/")
	if !ok || string(cached.data) != "icon" || cached.contentType != "image/png" {
		t.Fatalf("unexpected cached favicon: ok=%v value=%#v", ok, cached)
	}
	app.storeCachedFavicon("https://expired.example/", cachedFavicon{expiresAt: time.Now().Add(-time.Second)})
	if _, ok := app.loadCachedFavicon("https://expired.example/"); ok {
		t.Fatal("expected expired favicon to be evicted")
	}
}

func TestCollectOPMLURLsUsesParentAsGroup(t *testing.T) {
	outline := opmlOutline{
		Text: "Technology",
		Outlines: []opmlOutline{{
			Text:   "Example Feed",
			XMLURL: "https://example.com/feed.xml",
		}},
	}
	feeds := map[string]opmlFeed{}
	collectOPMLFeeds(outline, "Inbox", feeds)
	got := feeds["https://example.com/feed.xml"]
	if got.Group != "Technology" {
		t.Fatalf("expected parent outline to become group, got %q", got.Group)
	}
	if got.Title != "Example Feed" {
		t.Fatalf("expected OPML feed title to be retained, got %q", got.Title)
	}
}

func TestUnreadCountsAggregateByFeedAndGroup(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "feedss_test.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := initSchema(db); err != nil {
		t.Fatal(err)
	}

	app := &App{db: db}
	user, err := app.createUser("reader", "pw123", false)
	if err != nil {
		t.Fatal(err)
	}
	groupID := app.ensureGroup(user.ID, "Programming")
	result, err := db.Exec("INSERT INTO feeds(title, url, group_id, display_mode, sort_direction, created_by) VALUES(?, ?, ?, ?, ?, ?)", "Example", "https://example.com/rss", groupID, "headline", "desc", user.ID)
	if err != nil {
		t.Fatal(err)
	}
	feedID, _ := result.LastInsertId()
	for index, read := range []int{0, 0, 1} {
		if _, err := db.Exec("INSERT INTO articles(feed_id, title, guid, order_index, is_read) VALUES(?, ?, ?, ?, ?)", feedID, "Article", index, index, read); err != nil {
			t.Fatal(err)
		}
	}

	groups, err := app.listGroups(user.ID)
	if err != nil {
		t.Fatal(err)
	}
	feeds, err := app.listFeeds(user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(groups) != 1 || groups[0].UnreadCount != 2 || groups[0].FeedCount != 1 {
		t.Fatalf("unexpected group counts: %#v", groups)
	}
	if len(feeds) != 1 || feeds[0].UnreadCount != 2 {
		t.Fatalf("unexpected feed counts: %#v", feeds)
	}
}

func TestMarkFeedReadStopsAtListBoundary(t *testing.T) {
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "feedss_test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := initSchema(db); err != nil {
		t.Fatal(err)
	}
	app := &App{db: db}
	user, err := app.createUser("reader", "pw123", false)
	if err != nil {
		t.Fatal(err)
	}
	groupID := app.ensureGroup(user.ID, "Programming")
	result, err := db.Exec("INSERT INTO feeds(title, url, group_id, created_by) VALUES(?, ?, ?, ?)", "Example", "https://example.com/rss", groupID, user.ID)
	if err != nil {
		t.Fatal(err)
	}
	feedID, _ := result.LastInsertId()
	articleIDs := make([]int64, 0, 3)
	for index, orderIndex := range []int{100, 200, 150} {
		result, err := db.Exec("INSERT INTO articles(feed_id, title, guid, order_index) VALUES(?, ?, ?, ?)", feedID, "Article", index, orderIndex)
		if err != nil {
			t.Fatal(err)
		}
		articleID, _ := result.LastInsertId()
		articleIDs = append(articleIDs, articleID)
	}

	form := url.Values{
		"feed_id":                  {strconv.FormatInt(feedID, 10)},
		"read_through_order_index": {"200"},
		"read_through_id":          {strconv.FormatInt(articleIDs[1], 10)},
	}
	request := httptest.NewRequest(http.MethodPost, "/api/articles/read", strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	addSessionCookie(t, app, request, user)
	response := httptest.NewRecorder()
	app.handleArticleReadAPI(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("unexpected response %d: %s", response.Code, response.Body.String())
	}

	for index, articleID := range articleIDs {
		var isRead bool
		if err := db.QueryRow("SELECT is_read FROM articles WHERE id = ?", articleID).Scan(&isRead); err != nil {
			t.Fatal(err)
		}
		if want := index < 2; isRead != want {
			t.Fatalf("article %d read state = %v, want %v", articleID, isRead, want)
		}
	}
}

func TestArticleTitleFallsBackToDescriptionText(t *testing.T) {
	item := &gofeed.Item{Description: `<p><img src="photo.jpg"> A useful fallback title. </p>`}
	if got := articleTitle(item); got != "A useful fallback title." {
		t.Fatalf("unexpected fallback title %q", got)
	}
}

func TestArticleTitleNormalizesEntitiesAndInlineMarkup(t *testing.T) {
	tests := []struct {
		name  string
		title string
		want  string
	}{
		{name: "double encoded entity", title: "Claude&amp;#8217;s invisible watermark", want: "Claude’s invisible watermark"},
		{name: "inline formatting", title: "<b>Breaking</b>: <i>Important news</i>", want: "Breaking: Important news"},
		{name: "encoded inline formatting", title: "&lt;b&gt;Bold&lt;/b&gt; and &lt;i&gt;italic&lt;/i&gt;", want: "Bold and italic"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := articleTitle(&gofeed.Item{Title: test.title}); got != test.want {
				t.Fatalf("articleTitle() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestExtractCommentsLinkFromArticleHTML(t *testing.T) {
	item := &gofeed.Item{
		Description: `<a href="https://news.ycombinator.com/item?id=12345">Comments</a>`,
		Link:        "https://example.com/article",
	}
	if got := extractCommentsLink(item); got != "https://news.ycombinator.com/item?id=12345" {
		t.Fatalf("unexpected comments link %q", got)
	}
}

func TestStoredArticleMaintenanceBackfillsCommentsAndPrunesStaleRows(t *testing.T) {
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "feedss_test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := initSchema(db); err != nil {
		t.Fatal(err)
	}
	app := &App{db: db}
	user, err := app.createUser("reader", "pw123", false)
	if err != nil {
		t.Fatal(err)
	}
	groupID := app.ensureGroup(user.ID, "Programming")
	result, err := db.Exec("INSERT INTO feeds(title, url, group_id, created_by) VALUES(?, ?, ?, ?)", "Hacker News", "https://example.com/rss", groupID, user.ID)
	if err != nil {
		t.Fatal(err)
	}
	feedID, _ := result.LastInsertId()
	commentsHTML := `<a href="https://news.ycombinator.com/item?id=99">Comments</a>`
	if _, err := db.Exec("INSERT INTO articles(feed_id, title, description, published_at, guid) VALUES(?, ?, ?, ?, ?)", feedID, "<b>Recent&amp;#8217;s</b>", commentsHTML, time.Now().UTC().Format(time.RFC3339), "recent"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("INSERT INTO articles(feed_id, title, published_at, guid) VALUES(?, ?, ?, ?)", feedID, "Stale", time.Now().UTC().Add(-31*24*time.Hour).Format(time.RFC3339), "stale"); err != nil {
		t.Fatal(err)
	}
	if err := maintainStoredArticles(db); err != nil {
		t.Fatal(err)
	}
	var count int
	var commentsLink, title string
	if err := db.QueryRow("SELECT COUNT(*), COALESCE(MAX(comments_link), ''), COALESCE(MAX(title), '') FROM articles WHERE feed_id = ?", feedID).Scan(&count, &commentsLink, &title); err != nil {
		t.Fatal(err)
	}
	if count != 1 || commentsLink != "https://news.ycombinator.com/item?id=99" || title != "Recent’s" {
		t.Fatalf("unexpected maintained articles: count=%d comments=%q title=%q", count, commentsLink, title)
	}
}

func TestGroupArticlesIncludeAllFeedsInConfiguredOrder(t *testing.T) {
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "feedss_test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := initSchema(db); err != nil {
		t.Fatal(err)
	}
	app := &App{db: db}
	user, err := app.createUser("reader", "pw123", false)
	if err != nil {
		t.Fatal(err)
	}
	groupID := app.ensureGroup(user.ID, "Programming")
	feedIDs := make([]int64, 0, 2)
	for _, title := range []string{"Hacker News", "Lobsters"} {
		result, err := db.Exec("INSERT INTO feeds(title, url, group_id, display_mode, sort_direction, created_by) VALUES(?, ?, ?, 'headline', 'desc', ?)", title, "https://example.com/"+title, groupID, user.ID)
		if err != nil {
			t.Fatal(err)
		}
		feedID, _ := result.LastInsertId()
		feedIDs = append(feedIDs, feedID)
	}
	for index, order := range []int{100, 300, 200} {
		if _, err := db.Exec("INSERT INTO articles(feed_id, title, guid, order_index) VALUES(?, ?, ?, ?)", feedIDs[index%2], "Article", index, order); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec("UPDATE articles SET is_read = 1 WHERE order_index = 300"); err != nil {
		t.Fatal(err)
	}
	descending, err := app.listArticlesForGroup(user.ID, strconv.FormatInt(groupID, 10), "desc")
	if err != nil {
		t.Fatal(err)
	}
	ascending, err := app.listArticlesForGroup(user.ID, strconv.FormatInt(groupID, 10), "asc")
	if err != nil {
		t.Fatal(err)
	}
	if len(descending) != 3 || descending[0].OrderIndex != 300 || !descending[0].IsRead || descending[2].OrderIndex != 100 {
		t.Fatalf("unexpected descending group order: %#v", descending)
	}
	if len(ascending) != 3 || ascending[0].OrderIndex != 100 || ascending[2].OrderIndex != 300 || !ascending[2].IsRead {
		t.Fatalf("unexpected ascending group order: %#v", ascending)
	}
	page, total, err := app.listArticlesForGroupPage(user.ID, strconv.FormatInt(groupID, 10), "desc", 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	if total != 3 || len(page) != 1 || page[0].OrderIndex != 200 {
		t.Fatalf("unexpected group page: total=%d articles=%#v", total, page)
	}
	firstPage, total, hasMore, err := app.listArticlesForGroupCursorPage(user.ID, strconv.FormatInt(groupID, 10), "desc", 1, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if total != 3 || !hasMore || len(firstPage) != 1 || firstPage[0].OrderIndex != 300 {
		t.Fatalf("unexpected first cursor page: total=%d hasMore=%v articles=%#v", total, hasMore, firstPage)
	}
	if _, err := db.Exec("INSERT INTO articles(feed_id, title, guid, order_index) VALUES(?, 'New arrival', 'new-arrival', 400)", feedIDs[0]); err != nil {
		t.Fatal(err)
	}
	cursor := &articleCursor{OrderIndex: int64(firstPage[0].OrderIndex), ID: firstPage[0].ID}
	secondPage, _, _, err := app.listArticlesForGroupCursorPage(user.ID, strconv.FormatInt(groupID, 10), "desc", 1, cursor, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(secondPage) != 1 || secondPage[0].OrderIndex != 200 {
		t.Fatalf("cursor page admitted a newer article or skipped the next older article: %#v", secondPage)
	}
	unreadPage, unreadTotal, _, err := app.listArticlesForGroupCursorPage(user.ID, strconv.FormatInt(groupID, 10), "desc", 10, nil, true)
	if err != nil {
		t.Fatal(err)
	}
	if unreadTotal != 3 || len(unreadPage) != 3 {
		t.Fatalf("expected only three unread articles, total=%d articles=%#v", unreadTotal, unreadPage)
	}
}

func TestOPMLReimportMovesExistingFeedToParentGroup(t *testing.T) {
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "feedss_test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := initSchema(db); err != nil {
		t.Fatal(err)
	}
	app := &App{db: db}
	user, err := app.createUser("reader", "pw123", false)
	if err != nil {
		t.Fatal(err)
	}
	legacyGroupID := app.ensureGroup(user.ID, "Example Feed")
	if _, err := db.Exec("INSERT INTO feeds(title, url, group_id, display_mode, sort_direction, created_by) VALUES(?, ?, ?, ?, ?, ?)", "Example Feed", "https://example.com/feed.xml", legacyGroupID, "headline", "desc", user.ID); err != nil {
		t.Fatal(err)
	}
	opml := `<opml version="2.0"><body><outline text="Technology"><outline text="Example Feed" type="rss" xmlUrl="https://example.com/feed.xml"/></outline></body></opml>`
	request := httptest.NewRequest(http.MethodPost, "/api/import-opml", strings.NewReader(url.Values{"opml": {opml}}.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	addSessionCookie(t, app, request, user)
	response := httptest.NewRecorder()
	app.handleImportOPML(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("unexpected response %d: %s", response.Code, response.Body.String())
	}
	var groupName string
	if err := db.QueryRow("SELECT g.name FROM feeds f JOIN groups g ON g.id = f.group_id WHERE f.created_by = ? AND f.url = ?", user.ID, "https://example.com/feed.xml").Scan(&groupName); err != nil {
		t.Fatal(err)
	}
	if groupName != "Technology" {
		t.Fatalf("expected feed in Technology, got %q", groupName)
	}
	var legacyCount int
	if err := db.QueryRow("SELECT COUNT(*) FROM groups WHERE created_by = ? AND name = 'Example Feed'", user.ID).Scan(&legacyCount); err != nil {
		t.Fatal(err)
	}
	if legacyCount != 0 {
		t.Fatalf("expected empty legacy group to be removed")
	}
}

func TestManualRefreshWithNoFeeds(t *testing.T) {
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "feedss_test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := initSchema(db); err != nil {
		t.Fatal(err)
	}
	app := &App{db: db}
	user, err := app.createUser("reader", "pw123", false)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/refresh", nil)
	addSessionCookie(t, app, request, user)
	response := httptest.NewRecorder()
	app.handleRefreshAPI(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("unexpected response %d: %s", response.Code, response.Body.String())
	}
	var result map[string]int
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result["refreshed"] != 0 || result["failed"] != 0 {
		t.Fatalf("unexpected refresh result: %#v", result)
	}
}

func TestFeedRefreshStatusTracksFailureAndRecovery(t *testing.T) {
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "feedss_test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := initSchema(db); err != nil {
		t.Fatal(err)
	}
	app := &App{db: db}
	user, err := app.createUser("reader", "pw123", false)
	if err != nil {
		t.Fatal(err)
	}
	groupID := app.ensureGroup(user.ID, "Inbox")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/rss+xml")
		fmt.Fprint(w, `<?xml version="1.0"?><rss version="2.0"><channel><title>Working feed</title><item><title>Item</title><link>https://example.com/item</link><guid>item-1</guid></item></channel></rss>`)
	}))
	result, err := db.Exec("INSERT INTO feeds(title, url, group_id, created_by) VALUES('Example', ?, ?, ?)", server.URL, groupID, user.ID)
	if err != nil {
		t.Fatal(err)
	}
	feedID, _ := result.LastInsertId()
	if err := app.refreshFeed(user.ID, feedID); err != nil {
		t.Fatalf("successful refresh failed: %v", err)
	}
	server.Close()
	if err := app.refreshFeed(user.ID, feedID); err == nil {
		t.Fatal("expected refresh against closed server to fail")
	}
	feeds, err := app.listFeeds(user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(feeds) != 1 || feeds[0].LastRefreshError == "" || feeds[0].LastRefreshAt == "" || feeds[0].LastSuccessfulRefreshAt == "" {
		t.Fatalf("unexpected failed refresh status: %#v", feeds)
	}

	recovery := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/rss+xml")
		fmt.Fprint(w, `<?xml version="1.0"?><rss version="2.0"><channel><title>Recovered feed</title></channel></rss>`)
	}))
	defer recovery.Close()
	if _, err := db.Exec("UPDATE feeds SET url = ? WHERE id = ?", recovery.URL, feedID); err != nil {
		t.Fatal(err)
	}
	if err := app.refreshFeed(user.ID, feedID); err != nil {
		t.Fatalf("recovery refresh failed: %v", err)
	}
	feeds, err = app.listFeeds(user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if feeds[0].LastRefreshError != "" {
		t.Fatalf("successful refresh did not clear error: %#v", feeds[0])
	}
}

func TestDeleteFeedRemovesItsArticles(t *testing.T) {
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "feedss_test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := initSchema(db); err != nil {
		t.Fatal(err)
	}
	app := &App{db: db}
	user, err := app.createUser("reader", "pw123", false)
	if err != nil {
		t.Fatal(err)
	}
	groupID := app.ensureGroup(user.ID, "Inbox")
	result, err := db.Exec("INSERT INTO feeds(title, url, group_id, created_by) VALUES('Gone', 'https://example.com/rss', ?, ?)", groupID, user.ID)
	if err != nil {
		t.Fatal(err)
	}
	feedID, _ := result.LastInsertId()
	if _, err := db.Exec("INSERT INTO articles(feed_id, title, guid) VALUES(?, 'Old item', 'old-item')", feedID); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/feeds/delete", strings.NewReader(url.Values{"feed_id": {strconv.FormatInt(feedID, 10)}}.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	addSessionCookie(t, app, request, user)
	response := httptest.NewRecorder()
	app.handleDeleteFeedAPI(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("unexpected response %d: %s", response.Code, response.Body.String())
	}
	var feedCount, articleCount int
	if err := db.QueryRow("SELECT COUNT(*) FROM feeds WHERE id = ?", feedID).Scan(&feedCount); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow("SELECT COUNT(*) FROM articles WHERE feed_id = ?", feedID).Scan(&articleCount); err != nil {
		t.Fatal(err)
	}
	if feedCount != 0 || articleCount != 0 {
		t.Fatalf("expected feed and articles removed, feeds=%d articles=%d", feedCount, articleCount)
	}
}

func TestSavingDefaultsDoesNotOverwriteExistingFeedSettings(t *testing.T) {
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "feedss_test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := initSchema(db); err != nil {
		t.Fatal(err)
	}
	app := &App{db: db}
	user, err := app.createUser("admin", "pw123", true)
	if err != nil {
		t.Fatal(err)
	}
	groupID := app.ensureGroup(user.ID, "Programming")
	if _, err := db.Exec("INSERT INTO feeds(title, url, group_id, display_mode, sort_direction, created_by) VALUES('Example', 'https://example.com/rss', ?, 'headline', 'desc', ?)", groupID, user.ID); err != nil {
		t.Fatal(err)
	}
	form := url.Values{
		"refresh_interval_min":  {"15"},
		"max_articles_per_feed": {"500"},
		"default_display_mode":  {"full"},
		"default_sort_order":    {"asc"},
		"auto_refresh_enabled":  {"true"},
	}
	request := httptest.NewRequest(http.MethodPost, "/api/settings", strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	addSessionCookie(t, app, request, user)
	response := httptest.NewRecorder()
	app.handleSettingsAPI(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("unexpected response %d: %s", response.Code, response.Body.String())
	}
	var displayMode, sortDirection string
	if err := db.QueryRow("SELECT display_mode, sort_direction FROM feeds WHERE created_by = ?", user.ID).Scan(&displayMode, &sortDirection); err != nil {
		t.Fatal(err)
	}
	if displayMode != "headline" || sortDirection != "desc" {
		t.Fatalf("defaults overwrote existing feed settings: display=%q sort=%q", displayMode, sortDirection)
	}
}

func TestReleaseCheckSettingsPersist(t *testing.T) {
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "feedss_test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := initSchema(db); err != nil {
		t.Fatal(err)
	}
	app := &App{db: db}
	if err := ensureAppSettings(db); err != nil {
		t.Fatal(err)
	}
	user, err := app.createUser("admin", "pw123", true)
	if err != nil {
		t.Fatal(err)
	}
	form := url.Values{
		"refresh_interval_min":              {"15"},
		"max_articles_per_feed":             {"500"},
		"default_display_mode":              {"headline"},
		"default_sort_order":                {"desc"},
		"auto_refresh_enabled":              {"true"},
		"release_check_enabled":             {"false"},
		"release_check_include_prereleases": {"true"},
	}
	request := httptest.NewRequest(http.MethodPost, "/api/settings", strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	addSessionCookie(t, app, request, user)
	response := httptest.NewRecorder()
	app.handleSettingsAPI(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("unexpected response %d: %s", response.Code, response.Body.String())
	}
	settings, err := app.getSettings()
	if err != nil {
		t.Fatal(err)
	}
	if settings.ReleaseCheckEnabled || !settings.ReleaseCheckIncludePrereleases {
		t.Fatalf("release settings not persisted: %#v", settings)
	}
}

func TestFeedSettingsUpdateSelectedFeed(t *testing.T) {
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "feedss_test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := initSchema(db); err != nil {
		t.Fatal(err)
	}
	app := &App{db: db}
	user, err := app.createUser("reader", "pw123", false)
	if err != nil {
		t.Fatal(err)
	}
	groupID := app.ensureGroup(user.ID, "Programming")
	result, err := db.Exec("INSERT INTO feeds(title, url, group_id, display_mode, sort_direction, created_by) VALUES('Example', 'https://example.com/rss', ?, 'headline', 'desc', ?)", groupID, user.ID)
	if err != nil {
		t.Fatal(err)
	}
	feedID, _ := result.LastInsertId()
	form := url.Values{"feed_id": {strconv.FormatInt(feedID, 10)}, "display_mode": {"full"}, "sort_direction": {"asc"}}
	request := httptest.NewRequest(http.MethodPost, "/api/feeds/update", strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	addSessionCookie(t, app, request, user)
	response := httptest.NewRecorder()
	app.handleUpdateFeedAPI(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("unexpected response %d: %s", response.Code, response.Body.String())
	}
	var displayMode, sortDirection string
	if err := db.QueryRow("SELECT display_mode, sort_direction FROM feeds WHERE id = ?", feedID).Scan(&displayMode, &sortDirection); err != nil {
		t.Fatal(err)
	}
	if displayMode != "full" || sortDirection != "asc" {
		t.Fatalf("feed settings not updated: display=%q sort=%q", displayMode, sortDirection)
	}
}

func TestDefaultSettingsUse500ArticleRetention(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "feedss_test.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	if err := initSchema(db); err != nil {
		t.Fatal(err)
	}
	if err := ensureAppSettings(db); err != nil {
		t.Fatal(err)
	}

	var max int
	if err := db.QueryRow("SELECT max_articles_per_feed FROM app_settings WHERE id = 1").Scan(&max); err != nil {
		t.Fatal(err)
	}
	if max != 500 {
		t.Fatalf("expected default max articles per feed to be 500, got %d", max)
	}
}

func TestFirstLoginCreatesAdministratorWithoutDefaultAccount(t *testing.T) {
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "feedss_test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := initSchema(db); err != nil {
		t.Fatal(err)
	}
	app := &App{db: db}
	setup, err := app.needsInitialAdmin()
	if err != nil || !setup {
		t.Fatalf("expected first-run setup, setup=%v err=%v", setup, err)
	}
	form := url.Values{"username": {"owner"}, "password": {"chosen-password"}}
	request := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response := httptest.NewRecorder()
	app.handleLogin(response, request)
	if response.Code != http.StatusSeeOther || response.Header().Get("Location") != "/" {
		t.Fatalf("unexpected response %d location=%q body=%s", response.Code, response.Header().Get("Location"), response.Body.String())
	}
	user, err := app.lookupUser("owner", "chosen-password")
	if err != nil || !user.IsAdmin || user.MustChangePassword {
		t.Fatalf("unexpected initial administrator: %#v err=%v", user, err)
	}
	var inboxCount int
	if err := db.QueryRow("SELECT COUNT(*) FROM groups WHERE created_by = ? AND name = 'Inbox'", user.ID).Scan(&inboxCount); err != nil {
		t.Fatal(err)
	}
	if inboxCount != 1 {
		t.Fatalf("expected administrator Inbox group, got %d", inboxCount)
	}
}

func TestTemporaryUserMustChooseNewPassword(t *testing.T) {
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "feedss_test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := initSchema(db); err != nil {
		t.Fatal(err)
	}
	app := &App{db: db}
	admin, err := app.createUser("owner", "owner-password", true)
	if err != nil {
		t.Fatal(err)
	}
	form := url.Values{"username": {"reader"}, "temporary_password": {"temporary"}}
	request := httptest.NewRequest(http.MethodPost, "/api/users", strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	addSessionCookie(t, app, request, admin)
	response := httptest.NewRecorder()
	app.handleUsersAPI(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("unexpected create-user response %d: %s", response.Code, response.Body.String())
	}
	reader, err := app.lookupUser("reader", "temporary")
	if err != nil || !reader.MustChangePassword || reader.IsAdmin {
		t.Fatalf("unexpected temporary user: %#v err=%v", reader, err)
	}

	protected := app.requireAuth(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	request = httptest.NewRequest(http.MethodGet, "/", nil)
	addSessionCookie(t, app, request, reader)
	response = httptest.NewRecorder()
	protected.ServeHTTP(response, request)
	if response.Code != http.StatusSeeOther || response.Header().Get("Location") != "/change-password" {
		t.Fatalf("temporary user was not redirected to password change: %d %q", response.Code, response.Header().Get("Location"))
	}

	changeForm := url.Values{"new_password": {"permanent-password"}, "confirm_password": {"permanent-password"}}
	request = httptest.NewRequest(http.MethodPost, "/change-password", strings.NewReader(changeForm.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	addSessionCookie(t, app, request, reader)
	response = httptest.NewRecorder()
	app.handleChangePassword(response, request)
	if response.Code != http.StatusSeeOther || response.Header().Get("Location") != "/" {
		t.Fatalf("unexpected password-change response %d: %s", response.Code, response.Body.String())
	}
	if _, err := app.lookupUser("reader", "temporary"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("temporary password still works: %v", err)
	}
	reader, err = app.lookupUser("reader", "permanent-password")
	if err != nil || reader.MustChangePassword {
		t.Fatalf("new password was not activated: %#v err=%v", reader, err)
	}
}

func TestLegacyPlaintextPasswordIsUpgradedAfterLogin(t *testing.T) {
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "feedss_test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := initSchema(db); err != nil {
		t.Fatal(err)
	}
	result, err := db.Exec("INSERT INTO users(username, password, is_admin) VALUES('legacy', 'old-password', 1)")
	if err != nil {
		t.Fatal(err)
	}
	userID, _ := result.LastInsertId()
	app := &App{db: db}
	if _, err := app.lookupUser("legacy", "old-password"); err != nil {
		t.Fatalf("legacy login failed: %v", err)
	}
	var storedPassword string
	if err := db.QueryRow("SELECT password FROM users WHERE id = ?", userID).Scan(&storedPassword); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(storedPassword, "$2") || storedPassword == "old-password" {
		t.Fatalf("legacy password was not upgraded: %q", storedPassword)
	}
}

func TestUserCanChangeUsernameAndPassword(t *testing.T) {
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "feedss_test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := initSchema(db); err != nil {
		t.Fatal(err)
	}
	app := &App{db: db}
	user, err := app.createUser("reader", "old-password", false)
	if err != nil {
		t.Fatal(err)
	}
	form := url.Values{
		"username":         {"renamed-reader"},
		"current_password": {"old-password"},
		"new_password":     {"new-password"},
		"confirm_password": {"new-password"},
	}
	request := httptest.NewRequest(http.MethodPost, "/api/account", strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	addSessionCookie(t, app, request, user)
	response := httptest.NewRecorder()
	app.handleAccountAPI(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("unexpected account response %d: %s", response.Code, response.Body.String())
	}
	if _, err := app.lookupUser("reader", "old-password"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("old login still works: %v", err)
	}
	updated, err := app.lookupUser("renamed-reader", "new-password")
	if err != nil || updated.ID != user.ID {
		t.Fatalf("updated login failed: %#v err=%v", updated, err)
	}
	var authenticated bool
	for _, cookie := range response.Result().Cookies() {
		if cookie.Name == "feedss_user" && cookie.MaxAge > 0 {
			check := httptest.NewRequest(http.MethodGet, "/", nil)
			check.AddCookie(cookie)
			session, ok := app.getSession(check)
			authenticated = ok && session.Username == "renamed-reader"
		}
	}
	if !authenticated {
		t.Fatal("updated session cookie was not rotated")
	}
}

func TestUserIsolation(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "feedss_test.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	if err := initSchema(db); err != nil {
		t.Fatal(err)
	}

	app := &App{db: db}
	admin, err := app.createUser("admin", "admin123", true)
	if err != nil {
		t.Fatal(err)
	}
	alice, err := app.createUser("alice", "pw123", false)
	if err != nil {
		t.Fatal(err)
	}
	bob, err := app.createUser("bob", "pw456", false)
	if err != nil {
		t.Fatal(err)
	}

	adminGroup := app.ensureGroup(admin.ID, "Home")
	aliceGroup := app.ensureGroup(alice.ID, "Tech")
	bobGroup := app.ensureGroup(bob.ID, "News")

	if _, err := db.Exec("INSERT INTO feeds(title, url, group_id, display_mode, sort_direction, created_by) VALUES(?, ?, ?, ?, ?, ?)", "Admin Feed", "https://example.com/admin.xml", adminGroup, "headline", "desc", admin.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("INSERT INTO feeds(title, url, group_id, display_mode, sort_direction, created_by) VALUES(?, ?, ?, ?, ?, ?)", "Alice Feed", "https://example.com/alice.xml", aliceGroup, "headline", "asc", alice.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("INSERT INTO feeds(title, url, group_id, display_mode, sort_direction, created_by) VALUES(?, ?, ?, ?, ?, ?)", "Bob Feed", "https://example.com/bob.xml", bobGroup, "headline", "desc", bob.ID); err != nil {
		t.Fatal(err)
	}

	adminFeeds, err := app.listFeeds(admin.ID)
	if err != nil {
		t.Fatal(err)
	}
	aliceFeeds, err := app.listFeeds(alice.ID)
	if err != nil {
		t.Fatal(err)
	}

	if len(adminFeeds) != 1 || adminFeeds[0].Title != "Admin Feed" {
		t.Fatalf("admin should only see admin feeds, got %#v", adminFeeds)
	}
	if len(aliceFeeds) != 1 || aliceFeeds[0].Title != "Alice Feed" {
		t.Fatalf("alice should only see alice feeds, got %#v", aliceFeeds)
	}
}
