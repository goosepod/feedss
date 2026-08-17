package main

import (
	"database/sql"
	"encoding/json"
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
	for _, field := range []string{"\"id\"", "\"feed_count\"", "\"unread_count\"", "\"group_id\"", "\"display_mode\"", "\"feed_title\"", "\"comments_link\"", "\"is_read\"", "\"refresh_interval_min\"", "\"auto_refresh_enabled\""} {
		if !strings.Contains(jsonText, field) {
			t.Fatalf("expected API JSON to contain %s, got %s", field, jsonText)
		}
	}
	if strings.Contains(jsonText, "\"ID\"") || strings.Contains(jsonText, "\"GroupID\"") {
		t.Fatalf("API JSON leaked Go field names: %s", jsonText)
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

func TestArticleTitleFallsBackToDescriptionText(t *testing.T) {
	item := &gofeed.Item{Description: `<p><img src="photo.jpg"> A useful fallback title. </p>`}
	if got := articleTitle(item); got != "A useful fallback title." {
		t.Fatalf("unexpected fallback title %q", got)
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
	if _, err := db.Exec("INSERT INTO articles(feed_id, title, description, published_at, guid) VALUES(?, ?, ?, ?, ?)", feedID, "Recent", commentsHTML, time.Now().UTC().Format(time.RFC3339), "recent"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("INSERT INTO articles(feed_id, title, published_at, guid) VALUES(?, ?, ?, ?)", feedID, "Stale", time.Now().UTC().Add(-31*24*time.Hour).Format(time.RFC3339), "stale"); err != nil {
		t.Fatal(err)
	}
	if err := maintainStoredArticles(db); err != nil {
		t.Fatal(err)
	}
	var count int
	var commentsLink string
	if err := db.QueryRow("SELECT COUNT(*), COALESCE(MAX(comments_link), '') FROM articles WHERE feed_id = ?", feedID).Scan(&count, &commentsLink); err != nil {
		t.Fatal(err)
	}
	if count != 1 || commentsLink != "https://news.ycombinator.com/item?id=99" {
		t.Fatalf("unexpected maintained articles: count=%d comments=%q", count, commentsLink)
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
	if len(descending) != 3 || descending[0].OrderIndex != 200 || descending[2].OrderIndex != 300 || !descending[2].IsRead {
		t.Fatalf("unexpected descending group order: %#v", descending)
	}
	if len(ascending) != 3 || ascending[0].OrderIndex != 100 || ascending[2].OrderIndex != 300 || !ascending[2].IsRead {
		t.Fatalf("unexpected ascending group order: %#v", ascending)
	}
	page, total, err := app.listArticlesForGroupPage(user.ID, strconv.FormatInt(groupID, 10), "desc", 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	if total != 3 || len(page) != 1 || page[0].OrderIndex != 100 {
		t.Fatalf("unexpected group page: total=%d articles=%#v", total, page)
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
	request.AddCookie(&http.Cookie{Name: "feedss_user", Value: "1|reader|0"})
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
	if _, err := app.createUser("reader", "pw123", false); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/refresh", nil)
	request.AddCookie(&http.Cookie{Name: "feedss_user", Value: "1|reader|0"})
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
	request.AddCookie(&http.Cookie{Name: "feedss_user", Value: "1|admin|1"})
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
	request.AddCookie(&http.Cookie{Name: "feedss_user", Value: "1|reader|0"})
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
	if err := ensureAdminUser(db); err != nil {
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
