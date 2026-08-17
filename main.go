package main

import (
	"context"
	"database/sql"
	"embed"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"html/template"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/mmcdole/gofeed"
	"golang.org/x/net/html"
	_ "modernc.org/sqlite"
)

//go:embed templates/*
var templateFS embed.FS

//go:embed static/*
var staticFS embed.FS

const defaultPort = "4317"

const articleFreshnessWindow = 30 * 24 * time.Hour

var version = "dev"

// AppConfig holds runtime settings.
type AppConfig struct {
	DBPath string
	Port   string
}

// User stores local account info.
type User struct {
	ID        int64
	Username  string
	Password  string
	IsAdmin   bool
	CreatedAt string
}

// FeedGroup groups feeds in the UI.
type FeedGroup struct {
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	CreatedBy   int64  `json:"created_by"`
	CreatedAt   string `json:"created_at"`
	FeedCount   int    `json:"feed_count"`
	UnreadCount int    `json:"unread_count"`
	Selected    bool   `json:"selected"`
}

// Feed defines a feed source.
type Feed struct {
	ID            int64  `json:"id"`
	Title         string `json:"title"`
	URL           string `json:"url"`
	GroupID       int64  `json:"group_id"`
	DisplayMode   string `json:"display_mode"`
	SortDirection string `json:"sort_direction"`
	CreatedBy     int64  `json:"created_by"`
	CreatedAt     string `json:"created_at"`
	UnreadCount   int    `json:"unread_count"`
	Selected      bool   `json:"selected"`
}

// Article describes a feed item.
type Article struct {
	ID           int64     `json:"id"`
	FeedID       int64     `json:"feed_id"`
	FeedTitle    string    `json:"feed_title"`
	Title        string    `json:"title"`
	Link         string    `json:"link"`
	CommentsLink string    `json:"comments_link"`
	Description  string    `json:"description"`
	Content      string    `json:"content"`
	PublishedAt  time.Time `json:"published_at"`
	GUID         string    `json:"guid"`
	MediaURL     string    `json:"media_url"`
	OrderIndex   int       `json:"order_index"`
	IsRead       bool      `json:"is_read"`
}

// ArticlePage bounds reader responses while retaining the total result count.
type ArticlePage struct {
	Articles []Article `json:"articles"`
	Total    int       `json:"total"`
}

// App stores runtime state.
type App struct {
	db     *sql.DB
	config AppConfig
	tmpl   *template.Template
}

// AppSettings stores simple, admin-editable runtime defaults.
type AppSettings struct {
	ID                 int64  `json:"id"`
	RefreshIntervalMin int    `json:"refresh_interval_min"`
	MaxArticlesPerFeed int    `json:"max_articles_per_feed"`
	DefaultDisplayMode string `json:"default_display_mode"`
	DefaultSortOrder   string `json:"default_sort_order"`
	AutoRefreshEnabled bool   `json:"auto_refresh_enabled"`
	UpdatedAt          string `json:"updated_at"`
}

func main() {
	cfg := AppConfig{
		DBPath: getenv("APP_DB_PATH", filepath.Join("data", "feedss.db")),
		Port:   getenv("APP_PORT", defaultPort),
	}
	if err := os.MkdirAll(filepath.Dir(cfg.DBPath), 0o755); err != nil {
		log.Fatal(err)
	}

	app, err := NewApp(cfg)
	if err != nil {
		log.Fatal(err)
	}
	defer app.db.Close()
	if !strings.EqualFold(getenv("APP_DISABLE_AUTO_REFRESH", "false"), "true") {
		go app.startBackgroundRefreshLoop()
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/", app.handleIndex)
	mux.HandleFunc("/login", app.handleLogin)
	mux.HandleFunc("/logout", app.handleLogout)
	mux.HandleFunc("/feed/add", app.handleAddFeed)
	mux.HandleFunc("/api/groups", app.handleGroupsAPI)
	mux.HandleFunc("/api/feeds", app.handleFeedsAPI)
	mux.HandleFunc("/api/feeds/update", app.handleUpdateFeedAPI)
	mux.HandleFunc("/api/articles", app.handleArticlesAPI)
	mux.HandleFunc("/api/articles/read", app.handleArticleReadAPI)
	mux.HandleFunc("/api/image", app.handleImageProxy)
	mux.HandleFunc("/api/refresh", app.handleRefreshAPI)
	mux.HandleFunc("/api/settings", app.handleSettingsAPI)
	mux.HandleFunc("/api/import-opml", app.handleImportOPML)
	mux.HandleFunc("/api/export-opml", app.handleExportOPML)
	staticHandler := http.FileServer(http.FS(staticFS))
	mux.Handle("/static/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-cache")
		staticHandler.ServeHTTP(w, r)
	}))

	log.Printf("Starting feedss %s on :%s", version, cfg.Port)
	log.Fatal(http.ListenAndServe(":"+cfg.Port, app.requireAuth(mux)))
}

func getenv(key, fallback string) string {
	if val := strings.TrimSpace(os.Getenv(key)); val != "" {
		return val
	}
	return fallback
}

func NewApp(cfg AppConfig) (*App, error) {
	db, err := sql.Open("sqlite", cfg.DBPath)
	if err != nil {
		return nil, err
	}

	if err := db.Ping(); err != nil {
		return nil, err
	}
	if _, err := db.Exec("PRAGMA journal_mode=WAL;"); err != nil {
		return nil, err
	}
	if _, err := db.Exec("PRAGMA synchronous=NORMAL;"); err != nil {
		return nil, err
	}
	if _, err := db.Exec("PRAGMA foreign_keys=ON;"); err != nil {
		return nil, err
	}
	if _, err := db.Exec("PRAGMA busy_timeout=5000;"); err != nil {
		return nil, err
	}

	if err := initSchema(db); err != nil {
		return nil, err
	}
	if err := maintainStoredArticles(db); err != nil {
		return nil, err
	}
	if err := ensureAdminUser(db); err != nil {
		return nil, err
	}

	tmpl := template.Must(template.ParseFS(templateFS, "templates/*.html"))
	return &App{db: db, config: cfg, tmpl: tmpl}, nil
}

func initSchema(db *sql.DB) error {
	queries := []string{
		`CREATE TABLE IF NOT EXISTS users (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			username TEXT NOT NULL UNIQUE,
			password TEXT NOT NULL,
			is_admin INTEGER NOT NULL DEFAULT 0,
			created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
		);`,
		`CREATE TABLE IF NOT EXISTS groups (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL,
			created_by INTEGER NOT NULL,
			created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
			FOREIGN KEY(created_by) REFERENCES users(id)
		);`,
		`CREATE TABLE IF NOT EXISTS feeds (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			title TEXT NOT NULL,
			url TEXT NOT NULL,
			group_id INTEGER NOT NULL,
			display_mode TEXT NOT NULL DEFAULT 'headline',
			sort_direction TEXT NOT NULL DEFAULT 'desc',
			created_by INTEGER NOT NULL,
			created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
			FOREIGN KEY(group_id) REFERENCES groups(id),
			FOREIGN KEY(created_by) REFERENCES users(id)
		);`,
		`CREATE TABLE IF NOT EXISTS articles (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			feed_id INTEGER NOT NULL,
			title TEXT NOT NULL,
			link TEXT,
			comments_link TEXT,
			description TEXT,
			content TEXT,
			published_at TEXT,
			guid TEXT,
			media_url TEXT,
			order_index INTEGER NOT NULL DEFAULT 0,
			is_read INTEGER NOT NULL DEFAULT 0,
			FOREIGN KEY(feed_id) REFERENCES feeds(id)
		);`,
		`CREATE TABLE IF NOT EXISTS app_settings (
			id INTEGER PRIMARY KEY CHECK (id = 1),
			refresh_interval_min INTEGER NOT NULL DEFAULT 15,
			max_articles_per_feed INTEGER NOT NULL DEFAULT 500,
			default_display_mode TEXT NOT NULL DEFAULT 'headline',
			default_sort_order TEXT NOT NULL DEFAULT 'desc',
			auto_refresh_enabled INTEGER NOT NULL DEFAULT 1,
			updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
		);`,
		`CREATE INDEX IF NOT EXISTS idx_groups_created_by ON groups(created_by, name);`,
		`CREATE INDEX IF NOT EXISTS idx_feeds_created_by_group ON feeds(created_by, group_id, title);`,
		`CREATE INDEX IF NOT EXISTS idx_art_feed_id_order ON articles(feed_id, order_index DESC, published_at DESC);`,
		`CREATE INDEX IF NOT EXISTS idx_articles_feed_id ON articles(feed_id);`,
		`CREATE INDEX IF NOT EXISTS idx_articles_published_at ON articles(published_at);`,
	}

	for _, q := range queries {
		if _, err := db.Exec(q); err != nil {
			return fmt.Errorf("schema init: %w", err)
		}
	}
	if err := ensureColumn(db, "articles", "is_read", "INTEGER NOT NULL DEFAULT 0"); err != nil {
		return err
	}
	return nil
}

func ensureColumn(db *sql.DB, table, column, definition string) error {
	rows, err := db.Query("PRAGMA table_info(" + table + ")")
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var name, columnType string
		var notNull, primaryKey int
		var defaultValue any
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			return err
		}
		if name == column {
			return nil
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	_, err = db.Exec("ALTER TABLE " + table + " ADD COLUMN " + column + " " + definition)
	return err
}

func ensureAdminUser(db *sql.DB) error {
	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM users").Scan(&count); err != nil {
		return err
	}
	if count > 0 {
		return nil
	}

	app := &App{db: db}
	user, err := app.createUser("admin", "admin123", true)
	if err != nil {
		return err
	}
	app.ensureGroup(user.ID, "Inbox")
	if _, err := db.Exec(
		"INSERT INTO app_settings(id, refresh_interval_min, max_articles_per_feed, default_display_mode, default_sort_order, auto_refresh_enabled) VALUES(1, 15, 500, 'headline', 'desc', 1)"); err != nil {
		return err
	}
	return nil
}

func (app *App) getSettings() (*AppSettings, error) {
	var s AppSettings
	err := app.db.QueryRow(
		"SELECT id, refresh_interval_min, max_articles_per_feed, default_display_mode, default_sort_order, auto_refresh_enabled, updated_at FROM app_settings WHERE id = 1",
	).Scan(&s.ID, &s.RefreshIntervalMin, &s.MaxArticlesPerFeed, &s.DefaultDisplayMode, &s.DefaultSortOrder, &s.AutoRefreshEnabled, &s.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &s, nil
}

func (app *App) saveSettings(settings AppSettings) error {
	_, err := app.db.Exec(
		"INSERT INTO app_settings(id, refresh_interval_min, max_articles_per_feed, default_display_mode, default_sort_order, auto_refresh_enabled, updated_at) VALUES(1, ?, ?, ?, ?, ?, ?) ON CONFLICT(id) DO UPDATE SET refresh_interval_min = excluded.refresh_interval_min, max_articles_per_feed = excluded.max_articles_per_feed, default_display_mode = excluded.default_display_mode, default_sort_order = excluded.default_sort_order, auto_refresh_enabled = excluded.auto_refresh_enabled, updated_at = excluded.updated_at",
		settings.RefreshIntervalMin,
		settings.MaxArticlesPerFeed,
		settings.DefaultDisplayMode,
		settings.DefaultSortOrder,
		boolToInt(settings.AutoRefreshEnabled),
		time.Now().UTC().Format(time.RFC3339),
	)
	return err
}

func (app *App) startBackgroundRefreshLoop() {
	for {
		settings, err := app.getSettings()
		if err == nil && settings.AutoRefreshEnabled {
			if settings.RefreshIntervalMin <= 0 {
				settings.RefreshIntervalMin = 15
			}
			if settings.MaxArticlesPerFeed <= 0 {
				settings.MaxArticlesPerFeed = 500
			}
			if err := app.refreshAllFeeds(); err != nil {
				log.Printf("background refresh: %v", err)
			}
			time.Sleep(time.Duration(settings.RefreshIntervalMin) * time.Minute)
			continue
		}
		time.Sleep(5 * time.Minute)
	}
}

func (app *App) refreshAllFeeds() error {
	rows, err := app.db.Query("SELECT id, created_by, url FROM feeds")
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		var id, userID int64
		var url string
		if err := rows.Scan(&id, &userID, &url); err != nil {
			return err
		}
		if err := app.refreshFeed(userID, id); err != nil {
			log.Printf("refresh feed %d: %v", id, err)
		}
	}
	return rows.Err()
}

func (app *App) createUser(username, password string, isAdmin bool) (*User, error) {
	res, err := app.db.Exec(
		"INSERT INTO users(username, password, is_admin) VALUES(?, ?, ?)",
		username,
		password,
		boolToInt(isAdmin),
	)
	if err != nil {
		return nil, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, err
	}
	return &User{ID: id, Username: username, Password: password, IsAdmin: isAdmin}, nil
}

func (app *App) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}

	page := struct {
		Title string
	}{
		Title: "feedss",
	}
	if err := app.tmpl.ExecuteTemplate(w, "index.html", page); err != nil {
		log.Printf("template error: %v", err)
	}
}

func (app *App) handleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		if err := app.tmpl.ExecuteTemplate(w, "login.html", nil); err != nil {
			log.Printf("template error: %v", err)
		}
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}
	username := strings.TrimSpace(r.FormValue("username"))
	password := r.FormValue("password")
	if username == "" || password == "" {
		http.Error(w, "username and password are required", http.StatusBadRequest)
		return
	}

	user, err := app.lookupUser(username, password)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			http.Error(w, "invalid credentials", http.StatusUnauthorized)
			return
		}
		http.Error(w, "login failed", http.StatusInternalServerError)
		return
	}

	setSession(w, user)
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (app *App) handleLogout(w http.ResponseWriter, r *http.Request) {
	clearSession(w)
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

func (app *App) lookupUser(username, password string) (*User, error) {
	var user User
	err := app.db.QueryRow(
		"SELECT id, username, password, is_admin, created_at FROM users WHERE username = ? AND password = ?",
		username,
		password,
	).Scan(&user.ID, &user.Username, &user.Password, &user.IsAdmin, &user.CreatedAt)
	if err != nil {
		return nil, err
	}
	return &user, nil
}

func (app *App) requireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/login" || r.URL.Path == "/logout" || strings.HasPrefix(r.URL.Path, "/static/") {
			next.ServeHTTP(w, r)
			return
		}
		user, ok := getSession(r)
		if !ok {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		if user == nil {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func setSession(w http.ResponseWriter, user *User) {
	cookie := &http.Cookie{
		Name:     "feedss_user",
		Value:    fmt.Sprintf("%d|%s|%d", user.ID, user.Username, boolToInt(user.IsAdmin)),
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   86400 * 7,
	}
	http.SetCookie(w, cookie)
}

func clearSession(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{Name: "feedss_user", Value: "", Path: "/", MaxAge: -1})
}

func getSession(r *http.Request) (*User, bool) {
	cookie, err := r.Cookie("feedss_user")
	if err != nil {
		return nil, false
	}
	parts := strings.SplitN(cookie.Value, "|", 3)
	if len(parts) != 3 {
		return nil, false
	}
	userID := strings.TrimSpace(parts[0])
	if userID == "" {
		return nil, false
	}
	user := &User{ID: 1, Username: parts[1], IsAdmin: intToBool(parts[2])}
	if parsedID, err := strconv.ParseInt(userID, 10, 64); err == nil {
		user.ID = parsedID
	}
	return user, true
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func intToBool(s string) bool {
	return s == "1"
}

func (app *App) handleAddFeed(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}
	user, ok := getSession(r)
	if !ok || user == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	url := strings.TrimSpace(r.FormValue("url"))
	if url == "" {
		http.Error(w, "feed URL is required", http.StatusBadRequest)
		return
	}
	groupID := app.ensureGroup(user.ID, "Inbox")
	if groupName := strings.TrimSpace(r.FormValue("group")); groupName != "" {
		groupID = app.ensureGroup(user.ID, groupName)
	}
	settings, err := app.getSettings()
	if err != nil {
		settings = &AppSettings{DefaultDisplayMode: "headline", DefaultSortOrder: "desc"}
	}
	feedTitle, _ := app.fetchFeedTitle(url)
	feedDisplay := strings.TrimSpace(r.FormValue("display_mode"))
	if feedDisplay == "" {
		feedDisplay = settings.DefaultDisplayMode
	}
	feedOrder := strings.TrimSpace(r.FormValue("sort_direction"))
	if feedOrder == "" {
		feedOrder = settings.DefaultSortOrder
	}

	if _, err := app.db.Exec(
		"INSERT INTO feeds(title, url, group_id, display_mode, sort_direction, created_by) VALUES(?, ?, ?, ?, ?, ?)",
		feedTitle,
		url,
		groupID,
		feedDisplay,
		feedOrder,
		user.ID,
	); err != nil {
		log.Printf("insert feed: %v", err)
		http.Error(w, "failed to add feed", http.StatusInternalServerError)
		return
	}

	jsonSuccess(w, map[string]string{"status": "ok"})
}

func (app *App) ensureGroup(userID int64, groupName string) int64 {
	var id int64
	err := app.db.QueryRow("SELECT id FROM groups WHERE created_by = ? AND name = ?", userID, groupName).Scan(&id)
	if err == nil {
		return id
	}
	res, err := app.db.Exec("INSERT INTO groups(name, created_by) VALUES(?, ?)", groupName, userID)
	if err != nil {
		log.Printf("insert group: %v", err)
		return 1
	}
	id, _ = res.LastInsertId()
	return id
}

func (app *App) fetchFeedTitle(url string) (string, error) {
	fp := gofeed.NewParser()
	feed, err := fp.ParseURL(url)
	if err != nil {
		return "Untitled Feed", err
	}
	if feed.Title != "" {
		return feed.Title, nil
	}
	return "Untitled Feed", nil
}

func normalizePublishedTime(rawValue string, parsed *time.Time) time.Time {
	if parsed != nil && !parsed.IsZero() {
		return *parsed
	}
	if rawValue == "" {
		return time.Now().UTC()
	}
	if ts, err := time.Parse(time.RFC3339, rawValue); err == nil {
		return ts.UTC()
	}
	if ts, err := time.Parse("2006-01-02 15:04:05", rawValue); err == nil {
		return ts.UTC()
	}
	if ts, err := time.Parse(time.RFC1123, rawValue); err == nil {
		return ts.UTC()
	}
	return time.Now().UTC()
}

func extractCommentsLink(item *gofeed.Item) string {
	if item == nil {
		return ""
	}
	for _, link := range item.Links {
		if strings.Contains(strings.ToLower(link), "comments") {
			return link
		}
	}
	if item.GUID != "" && strings.Contains(strings.ToLower(item.GUID), "comments") {
		return item.GUID
	}
	if link := extractCommentsLinkFromHTML(item.Description); link != "" {
		return link
	}
	if link := extractCommentsLinkFromHTML(item.Content); link != "" {
		return link
	}
	return ""
}

func extractCommentsLinkFromHTML(rawHTML string) string {
	z := html.NewTokenizer(strings.NewReader(rawHTML))
	anchorHref := ""
	for {
		switch z.Next() {
		case html.ErrorToken:
			return ""
		case html.StartTagToken:
			token := z.Token()
			if token.Data != "a" {
				continue
			}
			anchorHref = ""
			for _, attribute := range token.Attr {
				if attribute.Key == "href" {
					anchorHref = strings.TrimSpace(attribute.Val)
					break
				}
			}
			if strings.Contains(strings.ToLower(anchorHref), "news.ycombinator.com/item?id=") {
				return anchorHref
			}
		case html.TextToken:
			if anchorHref != "" && strings.Contains(strings.ToLower(string(z.Text())), "comment") {
				return anchorHref
			}
		case html.EndTagToken:
			if z.Token().Data == "a" {
				anchorHref = ""
			}
		}
	}
}

func maintainStoredArticles(db *sql.DB) error {
	cutoff := time.Now().UTC().Add(-articleFreshnessWindow).Format(time.RFC3339)
	if _, err := db.Exec("DELETE FROM articles WHERE published_at IS NOT NULL AND published_at < ?", cutoff); err != nil {
		return err
	}
	rows, err := db.Query("SELECT id, COALESCE(description, ''), COALESCE(content, '') FROM articles WHERE COALESCE(comments_link, '') = ''")
	if err != nil {
		return err
	}
	type commentsUpdate struct {
		id   int64
		link string
	}
	updates := make([]commentsUpdate, 0)
	for rows.Next() {
		var id int64
		var description, content string
		if err := rows.Scan(&id, &description, &content); err != nil {
			rows.Close()
			return err
		}
		link := extractCommentsLinkFromHTML(description)
		if link == "" {
			link = extractCommentsLinkFromHTML(content)
		}
		if link != "" {
			updates = append(updates, commentsUpdate{id: id, link: link})
		}
	}
	if err := rows.Close(); err != nil {
		return err
	}
	for _, update := range updates {
		if _, err := db.Exec("UPDATE articles SET comments_link = ? WHERE id = ?", update.link, update.id); err != nil {
			return err
		}
	}
	return nil
}

func articleTitle(item *gofeed.Item) string {
	if item == nil {
		return "Untitled article"
	}
	if title := strings.TrimSpace(item.Title); title != "" {
		return title
	}
	z := html.NewTokenizer(strings.NewReader(item.Description))
	parts := make([]string, 0, 4)
	for {
		switch z.Next() {
		case html.ErrorToken:
			text := strings.Join(strings.Fields(strings.Join(parts, " ")), " ")
			runes := []rune(text)
			if len(runes) > 100 {
				text = strings.TrimSpace(string(runes[:100])) + "..."
			}
			if text != "" {
				return text
			}
			return "Untitled article"
		case html.TextToken:
			if text := strings.TrimSpace(string(z.Text())); text != "" {
				parts = append(parts, text)
			}
		}
	}
}

func (app *App) refreshFeed(userID int64, feedID int64) error {
	var feed Feed
	err := app.db.QueryRow(
		"SELECT id, title, url, group_id, display_mode, sort_direction, created_by, created_at FROM feeds WHERE id = ? AND created_by = ?",
		feedID,
		userID,
	).Scan(&feed.ID, &feed.Title, &feed.URL, &feed.GroupID, &feed.DisplayMode, &feed.SortDirection, &feed.CreatedBy, &feed.CreatedAt)
	if err != nil {
		return err
	}

	fp := gofeed.NewParser()
	parsed, err := fp.ParseURL(feed.URL)
	if err != nil {
		return err
	}
	if parsed.Title != "" && feed.Title == "" {
		if _, err := app.db.Exec("UPDATE feeds SET title = ? WHERE id = ?", parsed.Title, feed.ID); err != nil {
			log.Printf("update feed title: %v", err)
		}
	}
	cutoff := time.Now().UTC().Add(-articleFreshnessWindow)
	if _, err := app.db.Exec("DELETE FROM articles WHERE feed_id = ? AND published_at IS NOT NULL AND published_at < ?", feedID, cutoff.Format(time.RFC3339)); err != nil {
		return err
	}
	newest := time.Time{}
	for _, item := range parsed.Items {
		published := normalizePublishedTime(item.Published, item.PublishedParsed)
		if published.After(newest) {
			newest = published
		}
	}
	if !newest.IsZero() && newest.Before(cutoff) {
		return nil
	}

	type existingArticle struct {
		ID    int64
		Title string
	}
	seen := make(map[string]existingArticle)
	rows, err := app.db.Query("SELECT id, guid, link, title FROM articles WHERE feed_id = ?", feedID)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var guid, link, title sql.NullString
		if err := rows.Scan(&id, &guid, &link, &title); err != nil {
			return err
		}
		existing := existingArticle{ID: id, Title: title.String}
		if guid.Valid && guid.String != "" {
			seen[strings.TrimSpace(guid.String)] = existing
		}
		if link.Valid && link.String != "" {
			seen[strings.TrimSpace(link.String)] = existing
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}

	for _, item := range parsed.Items {
		key := strings.TrimSpace(item.GUID)
		if key == "" {
			if item.Link != "" {
				key = item.Link
			}
		}
		if key == "" && item.Title != "" {
			key = item.Title
		}
		if key == "" {
			continue
		}
		itemTitle := articleTitle(item)
		commentsLink := extractCommentsLink(item)
		if existing, exists := seen[key]; exists {
			if (strings.TrimSpace(existing.Title) == "" && itemTitle != "Untitled article") || commentsLink != "" {
				if _, err := app.db.Exec(
					`UPDATE articles
					SET title = CASE WHEN TRIM(COALESCE(title, '')) = '' THEN ? ELSE title END,
						comments_link = CASE WHEN COALESCE(comments_link, '') = '' THEN ? ELSE comments_link END
					WHERE id = ?`,
					itemTitle, commentsLink, existing.ID,
				); err != nil {
					return err
				}
			}
			continue
		}
		published := normalizePublishedTime(item.Published, item.PublishedParsed)
		if published.Before(cutoff) {
			continue
		}
		body := item.Content
		if body == "" {
			body = item.Description
		}
		link := item.Link
		if _, err := app.db.Exec(
			"INSERT INTO articles(feed_id, title, link, comments_link, description, content, published_at, guid, media_url, order_index) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
			feedID,
			itemTitle,
			link,
			commentsLink,
			item.Description,
			body,
			published.UTC().Format(time.RFC3339),
			key,
			"",
			published.UnixNano()/1_000_000,
		); err != nil {
			return err
		}
		seen[key] = existingArticle{Title: itemTitle}
	}

	settings, err := app.getSettings()
	if err != nil || settings.MaxArticlesPerFeed <= 0 {
		settings = &AppSettings{MaxArticlesPerFeed: 500}
	}
	var total int
	if err := app.db.QueryRow("SELECT COUNT(*) FROM articles WHERE feed_id = ?", feedID).Scan(&total); err != nil {
		return err
	}
	if total > settings.MaxArticlesPerFeed {
		deleteCount := total - settings.MaxArticlesPerFeed
		_, err = app.db.Exec(
			"DELETE FROM articles WHERE id IN (SELECT id FROM articles WHERE feed_id = ? ORDER BY order_index ASC, published_at ASC, id ASC LIMIT ?)",
			feedID,
			deleteCount,
		)
		if err != nil {
			return err
		}
	}
	return nil
}

func (app *App) handleGroupsAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	user, ok := getSession(r)
	if !ok || user == nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	groups, err := app.listGroups(user.ID)
	if err != nil {
		log.Printf("list groups: %v", err)
		http.Error(w, "failed to load groups", http.StatusInternalServerError)
		return
	}
	jsonSuccess(w, groups)
}

func (app *App) handleFeedsAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	user, ok := getSession(r)
	if !ok || user == nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	feeds, err := app.listFeeds(user.ID)
	if err != nil {
		log.Printf("list feeds: %v", err)
		http.Error(w, "failed to load feeds", http.StatusInternalServerError)
		return
	}
	jsonSuccess(w, feeds)
}

func (app *App) handleUpdateFeedAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	user, ok := getSession(r)
	if !ok || user == nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}
	feedID := strings.TrimSpace(r.FormValue("feed_id"))
	displayMode := strings.TrimSpace(r.FormValue("display_mode"))
	sortDirection := strings.TrimSpace(r.FormValue("sort_direction"))
	validDisplayModes := map[string]bool{"headline": true, "headline-blurb": true, "full": true}
	if feedID == "" || !validDisplayModes[displayMode] || (sortDirection != "asc" && sortDirection != "desc") {
		http.Error(w, "invalid feed settings", http.StatusBadRequest)
		return
	}
	result, err := app.db.Exec(
		"UPDATE feeds SET display_mode = ?, sort_direction = ? WHERE id = ? AND created_by = ?",
		displayMode, sortDirection, feedID, user.ID,
	)
	if err != nil {
		log.Printf("update feed settings: %v", err)
		http.Error(w, "failed to update feed", http.StatusInternalServerError)
		return
	}
	updated, _ := result.RowsAffected()
	if updated == 0 {
		http.Error(w, "feed not found", http.StatusNotFound)
		return
	}
	jsonSuccess(w, map[string]string{"status": "ok", "display_mode": displayMode, "sort_direction": sortDirection})
}

func (app *App) handleArticlesAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	user, ok := getSession(r)
	if !ok || user == nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	feedID := r.URL.Query().Get("feed_id")
	groupID := r.URL.Query().Get("group_id")
	limit := parseIntOrDefault(r.URL.Query().Get("limit"), 30)
	if limit < 1 {
		limit = 1
	}
	if limit > 100 {
		limit = 100
	}
	offset := parseIntOrDefault(r.URL.Query().Get("offset"), 0)
	if offset < 0 {
		offset = 0
	}
	if feedID == "" && groupID == "" {
		jsonSuccess(w, ArticlePage{Articles: []Article{}, Total: 0})
		return
	}
	var articles []Article
	var total int
	var err error
	if groupID != "" {
		sortDirection := "desc"
		if settings, settingsErr := app.getSettings(); settingsErr == nil {
			sortDirection = settings.DefaultSortOrder
		}
		articles, total, err = app.listArticlesForGroupPage(user.ID, groupID, sortDirection, limit, offset)
	} else {
		var sortDirection string
		err = app.db.QueryRow("SELECT sort_direction FROM feeds WHERE id = ? AND created_by = ?", feedID, user.ID).Scan(&sortDirection)
		if errors.Is(err, sql.ErrNoRows) {
			http.Error(w, "feed not found", http.StatusNotFound)
			return
		}
		if err == nil {
			articles, total, err = app.listArticlesForFeedPage(user.ID, feedID, sortDirection, limit, offset)
		}
	}
	if err != nil {
		log.Printf("list articles: %v", err)
		http.Error(w, "failed to load articles", http.StatusInternalServerError)
		return
	}
	jsonSuccess(w, ArticlePage{Articles: articles, Total: total})
}

func (app *App) handleArticleReadAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	user, ok := getSession(r)
	if !ok || user == nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}
	articleID := strings.TrimSpace(r.FormValue("article_id"))
	feedID := strings.TrimSpace(r.FormValue("feed_id"))
	groupID := strings.TrimSpace(r.FormValue("group_id"))
	var result sql.Result
	var err error
	if articleID != "" {
		result, err = app.db.Exec(
			"UPDATE articles SET is_read = 1 WHERE id = ? AND feed_id IN (SELECT id FROM feeds WHERE created_by = ?)",
			articleID, user.ID,
		)
	} else if feedID != "" {
		result, err = app.db.Exec(
			"UPDATE articles SET is_read = 1 WHERE feed_id = ? AND feed_id IN (SELECT id FROM feeds WHERE created_by = ?)",
			feedID, user.ID,
		)
	} else if groupID != "" {
		result, err = app.db.Exec(
			"UPDATE articles SET is_read = 1 WHERE feed_id IN (SELECT id FROM feeds WHERE group_id = ? AND created_by = ?)",
			groupID, user.ID,
		)
	} else {
		http.Error(w, "article_id, feed_id, or group_id is required", http.StatusBadRequest)
		return
	}
	if err != nil {
		log.Printf("mark articles read: %v", err)
		http.Error(w, "failed to update articles", http.StatusInternalServerError)
		return
	}
	updated, _ := result.RowsAffected()
	jsonSuccess(w, map[string]any{"status": "ok", "updated": updated})
}

const maxProxiedImageBytes = 12 << 20

func isBlockedImageIP(ip net.IP) bool {
	if ip == nil || ip.IsUnspecified() || ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsMulticast() {
		return true
	}
	if v4 := ip.To4(); v4 != nil && v4[0] == 100 && v4[1]&0xc0 == 64 {
		return true
	}
	return false
}

func resolvePublicImageHost(ctx context.Context, host string) (net.IP, error) {
	if ip := net.ParseIP(host); ip != nil {
		if isBlockedImageIP(ip) {
			return nil, errors.New("private image address")
		}
		return ip, nil
	}
	var lastErr error
	for _, network := range []string{"ip4", "ip6"} {
		addresses, err := net.DefaultResolver.LookupIP(ctx, network, host)
		if err != nil {
			lastErr = err
			continue
		}
		for _, address := range addresses {
			if !isBlockedImageIP(address) {
				return address, nil
			}
		}
	}
	if lastErr != nil {
		return nil, lastErr
	}
	return nil, errors.New("image host has no public address")
}

func parseRemoteImageURL(rawURL string) (*url.URL, error) {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || parsed.Hostname() == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return nil, errors.New("invalid image URL")
	}
	if parsed.User != nil {
		return nil, errors.New("image URL credentials are not allowed")
	}
	return parsed, nil
}

func imageProxyClient() *http.Client {
	dialer := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
	transport := &http.Transport{
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(address)
			if err != nil {
				return nil, err
			}
			ip, err := resolvePublicImageHost(ctx, host)
			if err != nil {
				return nil, err
			}
			return dialer.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
		},
		TLSHandshakeTimeout: 10 * time.Second,
	}
	return &http.Client{
		Transport: transport,
		Timeout:   20 * time.Second,
		CheckRedirect: func(request *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return errors.New("too many image redirects")
			}
			_, err := parseRemoteImageURL(request.URL.String())
			return err
		},
	}
}

func (app *App) handleImageProxy(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	remoteURL, err := parseRemoteImageURL(r.URL.Query().Get("url"))
	if err != nil {
		http.Error(w, "invalid image URL", http.StatusBadRequest)
		return
	}
	request, err := http.NewRequestWithContext(r.Context(), http.MethodGet, remoteURL.String(), nil)
	if err != nil {
		http.Error(w, "invalid image request", http.StatusBadRequest)
		return
	}
	request.Header.Set("Accept", "image/avif,image/webp,image/png,image/jpeg,image/gif,image/*;q=0.8")
	request.Header.Set("User-Agent", "feedss/1.0")
	response, err := imageProxyClient().Do(request)
	if err != nil {
		log.Printf("image proxy %s: %v", remoteURL.Hostname(), err)
		http.Error(w, "image unavailable", http.StatusBadGateway)
		return
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		http.Error(w, "image host returned an error", http.StatusBadGateway)
		return
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, maxProxiedImageBytes+1))
	if err != nil {
		http.Error(w, "failed to read image", http.StatusBadGateway)
		return
	}
	if len(data) > maxProxiedImageBytes {
		http.Error(w, "image is too large", http.StatusRequestEntityTooLarge)
		return
	}
	contentType := response.Header.Get("Content-Type")
	if !strings.HasPrefix(strings.ToLower(contentType), "image/") {
		contentType = http.DetectContentType(data)
	}
	if !strings.HasPrefix(strings.ToLower(contentType), "image/") {
		http.Error(w, "remote content is not an image", http.StatusUnsupportedMediaType)
		return
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Cache-Control", "private, max-age=86400")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

func (app *App) handleRefreshAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	user, ok := getSession(r)
	if !ok || user == nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	feeds, err := app.listFeeds(user.ID)
	if err != nil {
		log.Printf("list feeds for manual refresh: %v", err)
		http.Error(w, "failed to load feeds", http.StatusInternalServerError)
		return
	}
	refreshed := 0
	failed := 0
	for _, feed := range feeds {
		if err := app.refreshFeed(user.ID, feed.ID); err != nil {
			log.Printf("manual refresh feed %d: %v", feed.ID, err)
			failed++
			continue
		}
		refreshed++
	}
	jsonSuccess(w, map[string]int{"refreshed": refreshed, "failed": failed})
}

func (app *App) listGroups(userID int64) ([]FeedGroup, error) {
	rows, err := app.db.Query(
		`SELECT g.id, g.name, g.created_by, g.created_at,
			COUNT(DISTINCT f.id), COALESCE(SUM(CASE WHEN a.is_read = 0 THEN 1 ELSE 0 END), 0)
		FROM groups g
		LEFT JOIN feeds f ON f.group_id = g.id AND f.created_by = g.created_by
		LEFT JOIN articles a ON a.feed_id = f.id
		WHERE g.created_by = ?
		GROUP BY g.id, g.name, g.created_by, g.created_at
		ORDER BY g.name COLLATE NOCASE ASC`,
		userID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	groups := make([]FeedGroup, 0)
	for rows.Next() {
		var g FeedGroup
		if err := rows.Scan(&g.ID, &g.Name, &g.CreatedBy, &g.CreatedAt, &g.FeedCount, &g.UnreadCount); err != nil {
			return nil, err
		}
		groups = append(groups, g)
	}
	return groups, rows.Err()
}

func (app *App) listFeeds(userID int64) ([]Feed, error) {
	rows, err := app.db.Query(
		`SELECT f.id, f.title, f.url, f.group_id, f.display_mode, f.sort_direction, f.created_by, f.created_at,
			COALESCE(SUM(CASE WHEN a.is_read = 0 THEN 1 ELSE 0 END), 0)
		FROM feeds f
		LEFT JOIN articles a ON a.feed_id = f.id
		WHERE f.created_by = ?
		GROUP BY f.id, f.title, f.url, f.group_id, f.display_mode, f.sort_direction, f.created_by, f.created_at
		ORDER BY f.title COLLATE NOCASE ASC`,
		userID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	feeds := make([]Feed, 0)
	for rows.Next() {
		var f Feed
		if err := rows.Scan(&f.ID, &f.Title, &f.URL, &f.GroupID, &f.DisplayMode, &f.SortDirection, &f.CreatedBy, &f.CreatedAt, &f.UnreadCount); err != nil {
			return nil, err
		}
		feeds = append(feeds, f)
	}
	return feeds, rows.Err()
}

func (app *App) listArticlesForFeed(userID int64, feedID string) ([]Article, error) {
	articles, _, err := app.listArticlesForFeedPage(userID, feedID, "desc", 1_000_000, 0)
	return articles, err
}

func (app *App) listArticlesForFeedPage(userID int64, feedID, sortDirection string, limit, offset int) ([]Article, int, error) {
	direction := "DESC"
	if strings.EqualFold(sortDirection, "asc") {
		direction = "ASC"
	}
	var total int
	if err := app.db.QueryRow(
		"SELECT COUNT(*) FROM articles a JOIN feeds f ON f.id = a.feed_id WHERE a.feed_id = ? AND f.created_by = ?",
		feedID, userID,
	).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := app.db.Query(
		"SELECT a.id, a.feed_id, f.title, a.title, COALESCE(a.link, ''), COALESCE(a.comments_link, ''), COALESCE(a.description, ''), COALESCE(a.content, ''), a.published_at, COALESCE(a.guid, ''), COALESCE(a.media_url, ''), a.order_index, a.is_read FROM articles a JOIN feeds f ON f.id = a.feed_id WHERE a.feed_id = ? AND f.created_by = ? ORDER BY a.is_read ASC, a.order_index "+direction+", a.published_at "+direction+", a.id "+direction+" LIMIT ? OFFSET ?",
		feedID, userID, limit, offset,
	)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	articles := make([]Article, 0)
	for rows.Next() {
		var a Article
		var publishedAt sql.NullString
		if err := rows.Scan(&a.ID, &a.FeedID, &a.FeedTitle, &a.Title, &a.Link, &a.CommentsLink, &a.Description, &a.Content, &publishedAt, &a.GUID, &a.MediaURL, &a.OrderIndex, &a.IsRead); err != nil {
			return nil, 0, err
		}
		if publishedAt.Valid {
			a.PublishedAt, _ = time.Parse(time.RFC3339, publishedAt.String)
		}
		articles = append(articles, a)
	}
	return articles, total, rows.Err()
}

func (app *App) listArticlesForGroup(userID int64, groupID, sortDirection string) ([]Article, error) {
	articles, _, err := app.listArticlesForGroupPage(userID, groupID, sortDirection, 1_000_000, 0)
	return articles, err
}

func (app *App) listArticlesForGroupPage(userID int64, groupID, sortDirection string, limit, offset int) ([]Article, int, error) {
	direction := "DESC"
	if strings.EqualFold(sortDirection, "asc") {
		direction = "ASC"
	}
	var total int
	if err := app.db.QueryRow(
		"SELECT COUNT(*) FROM articles a JOIN feeds f ON f.id = a.feed_id WHERE f.group_id = ? AND f.created_by = ?",
		groupID, userID,
	).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := app.db.Query(
		`SELECT a.id, a.feed_id, f.title, a.title, COALESCE(a.link, ''), COALESCE(a.comments_link, ''),
			COALESCE(a.description, ''), COALESCE(a.content, ''), a.published_at, COALESCE(a.guid, ''),
			COALESCE(a.media_url, ''), a.order_index, a.is_read
		FROM articles a
		JOIN feeds f ON f.id = a.feed_id
		WHERE f.group_id = ? AND f.created_by = ?
		ORDER BY a.is_read ASC, a.order_index `+direction+`, a.published_at `+direction+`, a.id `+direction+`
		LIMIT ? OFFSET ?`,
		groupID, userID, limit, offset,
	)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	articles := make([]Article, 0)
	for rows.Next() {
		var article Article
		var publishedAt sql.NullString
		if err := rows.Scan(
			&article.ID, &article.FeedID, &article.FeedTitle, &article.Title, &article.Link,
			&article.CommentsLink, &article.Description, &article.Content, &publishedAt,
			&article.GUID, &article.MediaURL, &article.OrderIndex, &article.IsRead,
		); err != nil {
			return nil, 0, err
		}
		if publishedAt.Valid {
			article.PublishedAt, _ = time.Parse(time.RFC3339, publishedAt.String)
		}
		articles = append(articles, article)
	}
	return articles, total, rows.Err()
}

func (app *App) handleSettingsAPI(w http.ResponseWriter, r *http.Request) {
	user, ok := getSession(r)
	if !ok || user == nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if !user.IsAdmin {
		http.Error(w, "admin required", http.StatusForbidden)
		return
	}

	if r.Method == http.MethodGet {
		settings, err := app.getSettings()
		if err != nil {
			log.Printf("get settings: %v", err)
			settings = &AppSettings{RefreshIntervalMin: 15, MaxArticlesPerFeed: 500, DefaultDisplayMode: "headline", DefaultSortOrder: "desc", AutoRefreshEnabled: true}
		}
		jsonSuccess(w, settings)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}
	settings := AppSettings{
		RefreshIntervalMin: parseIntOrDefault(r.FormValue("refresh_interval_min"), 15),
		MaxArticlesPerFeed: parseIntOrDefault(r.FormValue("max_articles_per_feed"), 500),
		DefaultDisplayMode: strings.TrimSpace(r.FormValue("default_display_mode")),
		DefaultSortOrder:   strings.TrimSpace(r.FormValue("default_sort_order")),
		AutoRefreshEnabled: strings.EqualFold(strings.TrimSpace(r.FormValue("auto_refresh_enabled")), "true") || r.FormValue("auto_refresh_enabled") == "1",
	}
	if settings.DefaultDisplayMode == "" {
		settings.DefaultDisplayMode = "headline"
	}
	if settings.DefaultSortOrder == "" {
		settings.DefaultSortOrder = "desc"
	}
	if settings.RefreshIntervalMin <= 0 {
		settings.RefreshIntervalMin = 15
	}
	if settings.MaxArticlesPerFeed <= 0 {
		settings.MaxArticlesPerFeed = 500
	}
	if err := app.saveSettings(settings); err != nil {
		log.Printf("save settings: %v", err)
		http.Error(w, "failed to save settings", http.StatusInternalServerError)
		return
	}
	jsonSuccess(w, settings)
}

func parseIntOrDefault(raw string, fallback int) int {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return fallback
	}
	v, err := strconv.Atoi(trimmed)
	if err != nil {
		return fallback
	}
	return v
}

type opmlOutline struct {
	Text     string        `xml:"text,attr"`
	Title    string        `xml:"title,attr"`
	XMLURL   string        `xml:"xmlUrl,attr"`
	HTMLURL  string        `xml:"htmlUrl,attr"`
	Outlines []opmlOutline `xml:"outline"`
}

type opmlDocument struct {
	Body struct {
		Outlines []opmlOutline `xml:"outline"`
	} `xml:"body"`
}

type opmlFeed struct {
	URL   string
	Title string
	Group string
}

func collectOPMLFeeds(node opmlOutline, parentGroup string, feeds map[string]opmlFeed) {
	if node.XMLURL != "" {
		title := strings.TrimSpace(node.Title)
		if title == "" {
			title = strings.TrimSpace(node.Text)
		}
		url := strings.TrimSpace(node.XMLURL)
		feeds[url] = opmlFeed{URL: url, Title: title, Group: parentGroup}
	}
	childGroup := parentGroup
	if node.XMLURL == "" {
		childGroup = strings.TrimSpace(node.Title)
		if childGroup == "" {
			childGroup = strings.TrimSpace(node.Text)
		}
	}
	for _, child := range node.Outlines {
		collectOPMLFeeds(child, childGroup, feeds)
	}
}

func (app *App) handleImportOPML(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	user, ok := getSession(r)
	if !ok || user == nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	var data []byte
	if err := r.ParseMultipartForm(32 << 20); err == nil {
		if file, _, err := r.FormFile("file"); err == nil {
			defer file.Close()
			data, _ = io.ReadAll(file)
		}
	}
	if len(data) == 0 {
		data = []byte(r.FormValue("opml"))
	}
	if len(data) == 0 {
		http.Error(w, "OPML content is required", http.StatusBadRequest)
		return
	}

	var doc opmlDocument
	if err := xml.Unmarshal(data, &doc); err != nil {
		http.Error(w, "invalid OPML document", http.StatusBadRequest)
		return
	}

	feeds := map[string]opmlFeed{}
	for _, outline := range doc.Body.Outlines {
		collectOPMLFeeds(outline, "Inbox", feeds)
	}
	if len(feeds) == 0 {
		jsonSuccess(w, map[string]string{"status": "ok", "imported": "0", "message": "no feed URLs found"})
		return
	}

	imported := 0
	updated := 0
	for _, importedFeed := range feeds {
		trimmedURL := strings.TrimSpace(importedFeed.URL)
		if trimmedURL == "" {
			continue
		}
		groupName := strings.TrimSpace(importedFeed.Group)
		if groupName == "" {
			groupName = "Inbox"
		}
		groupID := app.ensureGroup(user.ID, groupName)

		var existingID int64
		err := app.db.QueryRow("SELECT id FROM feeds WHERE created_by = ? AND url = ? LIMIT 1", user.ID, trimmedURL).Scan(&existingID)
		if err == nil {
			if importedFeed.Title != "" {
				_, err = app.db.Exec("UPDATE feeds SET group_id = ?, title = ? WHERE id = ?", groupID, importedFeed.Title, existingID)
			} else {
				_, err = app.db.Exec("UPDATE feeds SET group_id = ? WHERE id = ?", groupID, existingID)
			}
			if err != nil {
				log.Printf("update imported OPML feed: %v", err)
				continue
			}
			updated++
			continue
		}
		if err != sql.ErrNoRows {
			log.Printf("import OPML duplicate check: %v", err)
			continue
		}

		settings, err := app.getSettings()
		if err != nil {
			settings = &AppSettings{DefaultDisplayMode: "headline", DefaultSortOrder: "desc"}
		}
		feedTitle := strings.TrimSpace(importedFeed.Title)
		if feedTitle == "" {
			feedTitle, _ = app.fetchFeedTitle(trimmedURL)
		}
		if _, err := app.db.Exec(
			"INSERT INTO feeds(title, url, group_id, display_mode, sort_direction, created_by) VALUES(?, ?, ?, ?, ?, ?)",
			feedTitle,
			trimmedURL,
			groupID,
			settings.DefaultDisplayMode,
			settings.DefaultSortOrder,
			user.ID,
		); err != nil {
			log.Printf("import OPML feed: %v", err)
			continue
		}
		imported++
	}
	if _, err := app.db.Exec(
		"DELETE FROM groups WHERE created_by = ? AND name <> 'Inbox' AND NOT EXISTS (SELECT 1 FROM feeds WHERE feeds.group_id = groups.id)",
		user.ID,
	); err != nil {
		log.Printf("clean empty OPML groups: %v", err)
	}
	jsonSuccess(w, map[string]any{"status": "ok", "imported": imported, "updated": updated})
}

func (app *App) handleExportOPML(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	user, ok := getSession(r)
	if !ok || user == nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	feeds, err := app.listFeeds(user.ID)
	if err != nil {
		log.Printf("export OPML: %v", err)
		http.Error(w, "failed to load feeds", http.StatusInternalServerError)
		return
	}

	type exportOutline struct {
		XMLName xml.Name        `xml:"outline"`
		Text    string          `xml:"text,attr"`
		Title   string          `xml:"title,attr,omitempty"`
		XMLURL  string          `xml:"xmlUrl,attr,omitempty"`
		Type    string          `xml:"type,attr,omitempty"`
		Feeds   []exportOutline `xml:"outline,omitempty"`
	}

	var body struct {
		XMLName  xml.Name        `xml:"body"`
		Outlines []exportOutline `xml:"outline"`
	}
	groups, err := app.listGroups(user.ID)
	if err != nil {
		http.Error(w, "failed to export groups", http.StatusInternalServerError)
		return
	}
	groupIndexes := make(map[int64]int)
	for _, group := range groups {
		groupIndexes[group.ID] = len(body.Outlines)
		body.Outlines = append(body.Outlines, exportOutline{Text: group.Name, Title: group.Name})
	}
	for _, feed := range feeds {
		if feed.URL == "" {
			continue
		}
		feedOutline := exportOutline{Text: feed.Title, Title: feed.Title, XMLURL: feed.URL, Type: "rss"}
		if index, ok := groupIndexes[feed.GroupID]; ok {
			body.Outlines[index].Feeds = append(body.Outlines[index].Feeds, feedOutline)
		} else {
			body.Outlines = append(body.Outlines, feedOutline)
		}
	}

	result := struct {
		XMLName xml.Name `xml:"opml"`
		Version string   `xml:"version,attr"`
		Head    struct {
			Title string `xml:"title"`
		} `xml:"head"`
		Body struct {
			Outlines []exportOutline `xml:"outline"`
		} `xml:"body"`
	}{
		Version: "2.0",
	}
	result.Head.Title = "feedss"
	result.Body.Outlines = body.Outlines

	xmlData, err := xml.MarshalIndent(result, "", "  ")
	if err != nil {
		log.Printf("marshal OPML: %v", err)
		http.Error(w, "failed to export OPML", http.StatusInternalServerError)
		return
	}
	xmlData = append([]byte(xml.Header), xmlData...)
	w.Header().Set("Content-Type", "application/xml; charset=utf-8")
	w.Header().Set("Content-Disposition", "attachment; filename=feedss.opml")
	_, _ = w.Write(xmlData)
}

func jsonSuccess(w http.ResponseWriter, payload any) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		log.Printf("json encode: %v", err)
	}
}
