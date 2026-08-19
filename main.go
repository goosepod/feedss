package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"embed"
	"encoding/base64"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	stdhtml "html"
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
	"sync"
	"time"
	"unicode"

	"github.com/mmcdole/gofeed"
	"golang.org/x/crypto/bcrypt"
	xhtml "golang.org/x/net/html"
	_ "modernc.org/sqlite"
)

//go:embed templates/*
var templateFS embed.FS

//go:embed static/*
var staticFS embed.FS

const defaultPort = "4317"

const articleFreshnessWindow = 30 * 24 * time.Hour
const maxFeedResponseBytes = 16 << 20
const feedRefreshWorkers = 4
const githubReleasesURL = "https://api.github.com/repos/goosepod/feedss/releases"
const githubProjectReleasesURL = "https://github.com/goosepod/feedss/releases"

var version = "dev"

// AppConfig holds runtime settings.
type AppConfig struct {
	DBPath string
	Port   string
}

// User stores local account info.
type User struct {
	ID                 int64  `json:"id"`
	Username           string `json:"username"`
	Password           string `json:"-"`
	IsAdmin            bool   `json:"is_admin"`
	MustChangePassword bool   `json:"must_change_password"`
	CreatedAt          string `json:"created_at"`
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
	ID                      int64  `json:"id"`
	Title                   string `json:"title"`
	URL                     string `json:"url"`
	GroupID                 int64  `json:"group_id"`
	DisplayMode             string `json:"display_mode"`
	SortDirection           string `json:"sort_direction"`
	CreatedBy               int64  `json:"created_by"`
	CreatedAt               string `json:"created_at"`
	UnreadCount             int    `json:"unread_count"`
	LastRefreshError        string `json:"last_refresh_error"`
	LastRefreshAt           string `json:"last_refresh_at"`
	LastSuccessfulRefreshAt string `json:"last_successful_refresh_at"`
	ETag                    string `json:"-"`
	LastModified            string `json:"-"`
	Selected                bool   `json:"selected"`
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
	IsSaved      bool      `json:"is_saved"`
}

// ArticlePage bounds reader responses while retaining the total result count.
type ArticlePage struct {
	Articles              []Article `json:"articles"`
	Total                 int       `json:"total"`
	HasMore               bool      `json:"has_more"`
	ReadThroughOrderIndex *int64    `json:"read_through_order_index,omitempty"`
	ReadThroughID         *int64    `json:"read_through_id,omitempty"`
}

type articleCursor struct {
	OrderIndex int64
	ID         int64
}

type feedRefreshTarget struct {
	ID     int64
	UserID int64
}

// App stores runtime state.
type App struct {
	db           *sql.DB
	config       AppConfig
	tmpl         *template.Template
	faviconMu    sync.RWMutex
	faviconCache map[string]cachedFavicon
}

type cachedFavicon struct {
	data        []byte
	contentType string
	expiresAt   time.Time
}

// AppSettings stores simple, admin-editable runtime defaults.
type AppSettings struct {
	ID                             int64  `json:"id"`
	RefreshIntervalMin             int    `json:"refresh_interval_min"`
	MaxArticlesPerFeed             int    `json:"max_articles_per_feed"`
	DefaultDisplayMode             string `json:"default_display_mode"`
	DefaultSortOrder               string `json:"default_sort_order"`
	AutoRefreshEnabled             bool   `json:"auto_refresh_enabled"`
	ReleaseCheckEnabled            bool   `json:"release_check_enabled"`
	ReleaseCheckIncludePrereleases bool   `json:"release_check_include_prereleases"`
	UpdatedAt                      string `json:"updated_at"`
}

type GitHubRelease struct {
	TagName     string `json:"tag_name"`
	Name        string `json:"name"`
	HTMLURL     string `json:"html_url"`
	Prerelease  bool   `json:"prerelease"`
	Draft       bool   `json:"draft"`
	PublishedAt string `json:"published_at"`
}

type ReleaseCheckResult struct {
	Enabled         bool           `json:"enabled"`
	UpdateAvailable bool           `json:"update_available"`
	CurrentVersion  string         `json:"current_version"`
	Release         *GitHubRelease `json:"release,omitempty"`
	ReleasesURL     string         `json:"releases_url"`
}

type LoginPage struct {
	Setup    bool
	Username string
	Error    string
}

func main() {
	// Keep the banner and structured startup notices on the same stream. Docker
	// does not preserve ordering when it merges stdout and stderr.
	log.SetOutput(os.Stdout)

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
	autoRefreshAllowed := !strings.EqualFold(getenv("APP_DISABLE_AUTO_REFRESH", "false"), "true")
	refreshInterval := 15
	autoRefreshEnabled := autoRefreshAllowed
	if settings, settingsErr := app.getSettings(); settingsErr == nil {
		if settings.RefreshIntervalMin > 0 {
			refreshInterval = settings.RefreshIntervalMin
		}
		autoRefreshEnabled = autoRefreshAllowed && settings.AutoRefreshEnabled
	}
	fmt.Print(startupBanner(cfg, autoRefreshEnabled, refreshInterval))
	log.Printf("Database initialized: %s", cfg.DBPath)
	if autoRefreshEnabled {
		log.Printf("Background refresh enabled (every %d minutes)", refreshInterval)
	} else {
		log.Printf("Background refresh disabled")
	}
	if autoRefreshAllowed {
		go app.startBackgroundRefreshLoop()
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/", app.handleIndex)
	mux.HandleFunc("/login", app.handleLogin)
	mux.HandleFunc("/logout", app.handleLogout)
	mux.HandleFunc("/change-password", app.handleChangePassword)
	mux.HandleFunc("/api/account", app.handleAccountAPI)
	mux.HandleFunc("/feed/add", app.handleAddFeed)
	mux.HandleFunc("/api/groups", app.handleGroupsAPI)
	mux.HandleFunc("/api/feeds", app.handleFeedsAPI)
	mux.HandleFunc("/api/feeds/update", app.handleUpdateFeedAPI)
	mux.HandleFunc("/api/feeds/delete", app.handleDeleteFeedAPI)
	mux.HandleFunc("/api/articles", app.handleArticlesAPI)
	mux.HandleFunc("/api/articles/read", app.handleArticleReadAPI)
	mux.HandleFunc("/api/articles/saved", app.handleArticleSavedAPI)
	mux.HandleFunc("/api/search", app.handleSearchAPI)
	mux.HandleFunc("/api/image", app.handleImageProxy)
	mux.HandleFunc("/api/favicon", app.handleFaviconProxy)
	mux.HandleFunc("/api/refresh", app.handleRefreshAPI)
	mux.HandleFunc("/api/settings", app.handleSettingsAPI)
	mux.HandleFunc("/api/releases/check", app.handleReleaseCheckAPI)
	mux.HandleFunc("/api/users", app.handleUsersAPI)
	mux.HandleFunc("/api/import-opml", app.handleImportOPML)
	mux.HandleFunc("/api/export-opml", app.handleExportOPML)
	mux.HandleFunc("/service-worker.js", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
		data, err := staticFS.ReadFile("static/service-worker.js")
		if err != nil {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(data)
	})
	staticHandler := http.FileServer(http.FS(staticFS))
	mux.Handle("/static/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-cache")
		staticHandler.ServeHTTP(w, r)
	}))

	log.Printf("Listening for HTTP requests at http://0.0.0.0:%s", cfg.Port)
	log.Printf("feedss is ready")
	log.Fatal(http.ListenAndServe(":"+cfg.Port, app.requireAuth(mux)))
}

func startupBanner(cfg AppConfig, autoRefreshEnabled bool, refreshInterval int) string {
	refreshStatus := "disabled"
	if autoRefreshEnabled {
		refreshStatus = fmt.Sprintf("enabled, every %d minutes", refreshInterval)
	}
	return fmt.Sprintf(`
  ███████╗███████╗███████╗██████╗ ███████╗███████╗
  ██╔════╝██╔════╝██╔════╝██╔══██╗██╔════╝██╔════╝
  █████╗  █████╗  █████╗  ██║  ██║███████╗███████╗
  ██╔══╝  ██╔══╝  ██╔══╝  ██║  ██║╚════██║╚════██║
  ██║     ███████╗███████╗██████╔╝███████║███████║
  ╚═╝     ╚══════╝╚══════╝╚═════╝ ╚══════╝╚══════╝

  A small, self-hosted RSS reader

  Version:        %s
  Database:       %s
  Listen address: http://0.0.0.0:%s
  Auto refresh:   %s

  Web:     http://localhost:%s
  GitHub:  https://github.com/goosepod/feedss
  License: GPL-3.0

`, version, cfg.DBPath, cfg.Port, refreshStatus, cfg.Port)
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
	if err := ensureAppSettings(db); err != nil {
		return nil, err
	}

	tmpl := template.Must(template.ParseFS(templateFS, "templates/*.html"))
	return &App{db: db, config: cfg, tmpl: tmpl, faviconCache: make(map[string]cachedFavicon)}, nil
}

func initSchema(db *sql.DB) error {
	queries := []string{
		`CREATE TABLE IF NOT EXISTS users (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			username TEXT NOT NULL UNIQUE,
			password TEXT NOT NULL,
			is_admin INTEGER NOT NULL DEFAULT 0,
			must_change_password INTEGER NOT NULL DEFAULT 0,
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
			last_refresh_error TEXT NOT NULL DEFAULT '',
			last_refresh_at TEXT,
			last_successful_refresh_at TEXT,
			etag TEXT NOT NULL DEFAULT '',
			last_modified TEXT NOT NULL DEFAULT '',
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
			is_saved INTEGER NOT NULL DEFAULT 0,
			FOREIGN KEY(feed_id) REFERENCES feeds(id)
		);`,
		`CREATE TABLE IF NOT EXISTS app_settings (
			id INTEGER PRIMARY KEY CHECK (id = 1),
			refresh_interval_min INTEGER NOT NULL DEFAULT 15,
			max_articles_per_feed INTEGER NOT NULL DEFAULT 500,
			default_display_mode TEXT NOT NULL DEFAULT 'headline',
			default_sort_order TEXT NOT NULL DEFAULT 'desc',
			auto_refresh_enabled INTEGER NOT NULL DEFAULT 1,
			release_check_enabled INTEGER NOT NULL DEFAULT 1,
			release_check_include_prereleases INTEGER NOT NULL DEFAULT 0,
			updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
		);`,
		`CREATE TABLE IF NOT EXISTS sessions (
			token_hash TEXT PRIMARY KEY,
			user_id INTEGER NOT NULL,
			expires_at TEXT NOT NULL,
			created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
			FOREIGN KEY(user_id) REFERENCES users(id) ON DELETE CASCADE
		);`,
		`CREATE INDEX IF NOT EXISTS idx_groups_created_by ON groups(created_by, name);`,
		`CREATE INDEX IF NOT EXISTS idx_feeds_created_by_group ON feeds(created_by, group_id, title);`,
		`CREATE INDEX IF NOT EXISTS idx_art_feed_id_order ON articles(feed_id, order_index DESC, published_at DESC);`,
		`CREATE INDEX IF NOT EXISTS idx_articles_feed_id ON articles(feed_id);`,
		`CREATE INDEX IF NOT EXISTS idx_articles_published_at ON articles(published_at);`,
		`CREATE INDEX IF NOT EXISTS idx_sessions_user_id ON sessions(user_id, expires_at);`,
	}

	for _, q := range queries {
		if _, err := db.Exec(q); err != nil {
			return fmt.Errorf("schema init: %w", err)
		}
	}
	return runMigrations(db)
}

func ensureAppSettings(db *sql.DB) error {
	_, err := db.Exec(
		"INSERT OR IGNORE INTO app_settings(id, refresh_interval_min, max_articles_per_feed, default_display_mode, default_sort_order, auto_refresh_enabled, release_check_enabled, release_check_include_prereleases) VALUES(1, 15, 500, 'headline', 'desc', 1, 1, 0)")
	return err
}

func (app *App) getSettings() (*AppSettings, error) {
	var s AppSettings
	err := app.db.QueryRow(
		"SELECT id, refresh_interval_min, max_articles_per_feed, default_display_mode, default_sort_order, auto_refresh_enabled, release_check_enabled, release_check_include_prereleases, updated_at FROM app_settings WHERE id = 1",
	).Scan(&s.ID, &s.RefreshIntervalMin, &s.MaxArticlesPerFeed, &s.DefaultDisplayMode, &s.DefaultSortOrder, &s.AutoRefreshEnabled, &s.ReleaseCheckEnabled, &s.ReleaseCheckIncludePrereleases, &s.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &s, nil
}

func (app *App) saveSettings(settings AppSettings) error {
	_, err := app.db.Exec(
		"INSERT INTO app_settings(id, refresh_interval_min, max_articles_per_feed, default_display_mode, default_sort_order, auto_refresh_enabled, release_check_enabled, release_check_include_prereleases, updated_at) VALUES(1, ?, ?, ?, ?, ?, ?, ?, ?) ON CONFLICT(id) DO UPDATE SET refresh_interval_min = excluded.refresh_interval_min, max_articles_per_feed = excluded.max_articles_per_feed, default_display_mode = excluded.default_display_mode, default_sort_order = excluded.default_sort_order, auto_refresh_enabled = excluded.auto_refresh_enabled, release_check_enabled = excluded.release_check_enabled, release_check_include_prereleases = excluded.release_check_include_prereleases, updated_at = excluded.updated_at",
		settings.RefreshIntervalMin,
		settings.MaxArticlesPerFeed,
		settings.DefaultDisplayMode,
		settings.DefaultSortOrder,
		boolToInt(settings.AutoRefreshEnabled),
		boolToInt(settings.ReleaseCheckEnabled),
		boolToInt(settings.ReleaseCheckIncludePrereleases),
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
	rows, err := app.db.Query("SELECT id, created_by FROM feeds")
	if err != nil {
		return err
	}
	targets := make([]feedRefreshTarget, 0)
	for rows.Next() {
		var target feedRefreshTarget
		if err := rows.Scan(&target.ID, &target.UserID); err != nil {
			rows.Close()
			return err
		}
		targets = append(targets, target)
	}
	if err := rows.Close(); err != nil {
		return err
	}
	app.refreshFeeds(targets)
	return nil
}

func (app *App) refreshFeeds(targets []feedRefreshTarget) (int, int) {
	jobs := make(chan feedRefreshTarget)
	results := make(chan error, len(targets))
	var workers sync.WaitGroup
	for range min(feedRefreshWorkers, len(targets)) {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for target := range jobs {
				err := app.refreshFeed(target.UserID, target.ID)
				if err != nil {
					log.Printf("refresh feed %d: %v", target.ID, err)
				}
				results <- err
			}
		}()
	}
	for _, target := range targets {
		jobs <- target
	}
	close(jobs)
	workers.Wait()
	close(results)
	refreshed, failed := 0, 0
	for err := range results {
		if err != nil {
			failed++
		} else {
			refreshed++
		}
	}
	return refreshed, failed
}

func (app *App) createUser(username, password string, isAdmin bool) (*User, error) {
	return app.createUserWithPasswordState(username, password, isAdmin, false)
}

func (app *App) createUserWithPasswordState(username, password string, isAdmin, mustChangePassword bool) (*User, error) {
	passwordHash, err := hashPassword(password)
	if err != nil {
		return nil, err
	}
	res, err := app.db.Exec(
		"INSERT INTO users(username, password, is_admin, must_change_password) VALUES(?, ?, ?, ?)",
		username,
		passwordHash,
		boolToInt(isAdmin),
		boolToInt(mustChangePassword),
	)
	if err != nil {
		return nil, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, err
	}
	return &User{ID: id, Username: username, Password: passwordHash, IsAdmin: isAdmin, MustChangePassword: mustChangePassword}, nil
}

func (app *App) handleIndex(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}

	page := struct {
		Title   string
		IsAdmin bool
	}{
		Title:   "feedss",
		IsAdmin: false,
	}
	if session, ok := app.getSession(r); ok {
		if user, err := app.userByID(session.ID); err == nil {
			page.IsAdmin = user.IsAdmin
		}
	}
	if err := app.tmpl.ExecuteTemplate(w, "index.html", page); err != nil {
		log.Printf("template error: %v", err)
	}
}

func (app *App) handleLogin(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	setup, err := app.needsInitialAdmin()
	if err != nil {
		http.Error(w, "login failed", http.StatusInternalServerError)
		return
	}
	if r.Method == http.MethodGet {
		app.renderLogin(w, http.StatusOK, LoginPage{Setup: setup})
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
		app.renderLogin(w, http.StatusBadRequest, LoginPage{Setup: setup, Username: username, Error: "Username and password are required."})
		return
	}
	if setup && len([]byte(password)) > 72 {
		app.renderLogin(w, http.StatusBadRequest, LoginPage{Setup: setup, Username: username, Error: "Password must be 72 bytes or fewer."})
		return
	}

	var user *User
	if setup {
		user, err = app.createInitialAdmin(username, password)
	} else {
		user, err = app.lookupUser(username, password)
	}
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			app.renderLogin(w, http.StatusUnauthorized, LoginPage{Setup: setup, Username: username, Error: "The username or password is incorrect."})
			return
		}
		http.Error(w, "login failed", http.StatusInternalServerError)
		return
	}

	if err := app.setSession(w, user); err != nil {
		http.Error(w, "login failed", http.StatusInternalServerError)
		return
	}
	if user.MustChangePassword {
		http.Redirect(w, r, "/change-password", http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (app *App) renderLogin(w http.ResponseWriter, status int, page LoginPage) {
	if status != http.StatusOK {
		w.WriteHeader(status)
	}
	if err := app.tmpl.ExecuteTemplate(w, "login.html", page); err != nil {
		log.Printf("template error: %v", err)
	}
}

func (app *App) needsInitialAdmin() (bool, error) {
	var count int
	if err := app.db.QueryRow("SELECT COUNT(*) FROM users").Scan(&count); err != nil {
		return false, err
	}
	return count == 0, nil
}

func (app *App) createInitialAdmin(username, password string) (*User, error) {
	passwordHash, err := hashPassword(password)
	if err != nil {
		return nil, err
	}
	tx, err := app.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var count int
	if err := tx.QueryRow("SELECT COUNT(*) FROM users").Scan(&count); err != nil {
		return nil, err
	}
	if count != 0 {
		return nil, errors.New("administrator already exists")
	}
	result, err := tx.Exec("INSERT INTO users(username, password, is_admin, must_change_password) VALUES(?, ?, 1, 0)", username, passwordHash)
	if err != nil {
		return nil, err
	}
	userID, err := result.LastInsertId()
	if err != nil {
		return nil, err
	}
	if _, err := tx.Exec("INSERT INTO groups(name, created_by) VALUES('Inbox', ?)", userID); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &User{ID: userID, Username: username, Password: passwordHash, IsAdmin: true}, nil
}

func (app *App) handleLogout(w http.ResponseWriter, r *http.Request) {
	app.clearSession(w, r)
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

func (app *App) lookupUser(username, password string) (*User, error) {
	var user User
	err := app.db.QueryRow(
		"SELECT id, username, password, is_admin, must_change_password, created_at FROM users WHERE username = ?",
		username,
	).Scan(&user.ID, &user.Username, &user.Password, &user.IsAdmin, &user.MustChangePassword, &user.CreatedAt)
	if err != nil {
		return nil, err
	}
	if strings.HasPrefix(user.Password, "$2") {
		if err := bcrypt.CompareHashAndPassword([]byte(user.Password), []byte(password)); err != nil {
			return nil, sql.ErrNoRows
		}
	} else {
		if user.Password != password {
			return nil, sql.ErrNoRows
		}
		if passwordHash, hashErr := hashPassword(password); hashErr == nil {
			if _, updateErr := app.db.Exec("UPDATE users SET password = ? WHERE id = ?", passwordHash, user.ID); updateErr == nil {
				user.Password = passwordHash
			}
		}
	}
	return &user, nil
}

func hashPassword(password string) (string, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return "", err
	}
	return string(hash), nil
}

func (app *App) userByID(userID int64) (*User, error) {
	var user User
	if err := app.db.QueryRow(
		"SELECT id, username, password, is_admin, must_change_password, created_at FROM users WHERE id = ?", userID,
	).Scan(&user.ID, &user.Username, &user.Password, &user.IsAdmin, &user.MustChangePassword, &user.CreatedAt); err != nil {
		return nil, err
	}
	return &user, nil
}

func (app *App) handleChangePassword(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	session, ok := app.getSession(r)
	if !ok || session == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	user, err := app.userByID(session.ID)
	if err != nil {
		app.clearSession(w, r)
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	if r.Method == http.MethodGet {
		if !user.MustChangePassword {
			http.Redirect(w, r, "/", http.StatusSeeOther)
			return
		}
		if err := app.tmpl.ExecuteTemplate(w, "change-password.html", user); err != nil {
			log.Printf("template error: %v", err)
		}
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	newPassword := r.FormValue("new_password")
	confirmation := r.FormValue("confirm_password")
	if newPassword == "" || newPassword != confirmation {
		http.Error(w, "passwords must be non-empty and match", http.StatusBadRequest)
		return
	}
	if len([]byte(newPassword)) > 72 {
		http.Error(w, "password must be 72 bytes or fewer", http.StatusBadRequest)
		return
	}
	passwordHash, err := hashPassword(newPassword)
	if err != nil {
		http.Error(w, "failed to change password", http.StatusInternalServerError)
		return
	}
	if _, err := app.db.Exec("UPDATE users SET password = ?, must_change_password = 0 WHERE id = ?", passwordHash, user.ID); err != nil {
		http.Error(w, "failed to change password", http.StatusInternalServerError)
		return
	}
	user.MustChangePassword = false
	app.clearSession(w, r)
	if err := app.setSession(w, user); err != nil {
		http.Error(w, "failed to create session", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (app *App) handleAccountAPI(w http.ResponseWriter, r *http.Request) {
	session, ok := app.getSession(r)
	if !ok || session == nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	user, err := app.userByID(session.ID)
	if err != nil {
		http.Error(w, "account not found", http.StatusNotFound)
		return
	}
	if r.Method == http.MethodGet {
		jsonSuccess(w, user)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	username := strings.TrimSpace(r.FormValue("username"))
	currentPassword := r.FormValue("current_password")
	newPassword := r.FormValue("new_password")
	confirmation := r.FormValue("confirm_password")
	if username == "" || currentPassword == "" {
		http.Error(w, "username and current password are required", http.StatusBadRequest)
		return
	}
	authenticated, err := app.lookupUser(user.Username, currentPassword)
	if err != nil {
		http.Error(w, "current password is incorrect", http.StatusUnauthorized)
		return
	}
	passwordHash := authenticated.Password
	if newPassword != "" {
		if newPassword != confirmation {
			http.Error(w, "new passwords do not match", http.StatusBadRequest)
			return
		}
		if len([]byte(newPassword)) > 72 {
			http.Error(w, "password must be 72 bytes or fewer", http.StatusBadRequest)
			return
		}
		passwordHash, err = hashPassword(newPassword)
		if err != nil {
			http.Error(w, "failed to update account", http.StatusInternalServerError)
			return
		}
	}
	if _, err := app.db.Exec("UPDATE users SET username = ?, password = ? WHERE id = ?", username, passwordHash, user.ID); err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			http.Error(w, "username already exists", http.StatusConflict)
			return
		}
		http.Error(w, "failed to update account", http.StatusInternalServerError)
		return
	}
	user.Username = username
	user.Password = passwordHash
	app.clearSession(w, r)
	if err := app.setSession(w, user); err != nil {
		http.Error(w, "failed to create session", http.StatusInternalServerError)
		return
	}
	jsonSuccess(w, user)
}

func (app *App) requireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/login" || r.URL.Path == "/logout" || r.URL.Path == "/service-worker.js" || strings.HasPrefix(r.URL.Path, "/static/") {
			next.ServeHTTP(w, r)
			return
		}
		session, ok := app.getSession(r)
		if !ok {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		if session == nil {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		user, err := app.userByID(session.ID)
		if err != nil {
			app.clearSession(w, r)
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		if user.MustChangePassword && r.URL.Path != "/change-password" {
			http.Redirect(w, r, "/change-password", http.StatusSeeOther)
			return
		}
		next.ServeHTTP(w, r)
	})
}

const sessionLifetime = 7 * 24 * time.Hour

func sessionTokenHash(token string) string {
	digest := sha256.Sum256([]byte(token))
	return base64.RawURLEncoding.EncodeToString(digest[:])
}

func (app *App) setSession(w http.ResponseWriter, user *User) error {
	randomBytes := make([]byte, 32)
	if _, err := rand.Read(randomBytes); err != nil {
		return err
	}
	token := base64.RawURLEncoding.EncodeToString(randomBytes)
	expiresAt := time.Now().UTC().Add(sessionLifetime)
	if _, err := app.db.Exec(
		"INSERT INTO sessions(token_hash, user_id, expires_at) VALUES(?, ?, ?)",
		sessionTokenHash(token), user.ID, expiresAt.Format(time.RFC3339Nano),
	); err != nil {
		return err
	}
	_, _ = app.db.Exec("DELETE FROM sessions WHERE expires_at <= ?", time.Now().UTC().Format(time.RFC3339Nano))
	cookie := &http.Cookie{
		Name:     "feedss_user",
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(sessionLifetime.Seconds()),
		Expires:  expiresAt,
	}
	http.SetCookie(w, cookie)
	return nil
}

func (app *App) clearSession(w http.ResponseWriter, r *http.Request) {
	if cookie, err := r.Cookie("feedss_user"); err == nil && cookie.Value != "" {
		_, _ = app.db.Exec("DELETE FROM sessions WHERE token_hash = ?", sessionTokenHash(cookie.Value))
	}
	http.SetCookie(w, &http.Cookie{
		Name: "feedss_user", Value: "", Path: "/", HttpOnly: true,
		SameSite: http.SameSiteLaxMode, MaxAge: -1,
	})
}

func (app *App) getSession(r *http.Request) (*User, bool) {
	cookie, err := r.Cookie("feedss_user")
	if err != nil || cookie.Value == "" {
		return nil, false
	}
	var user User
	err = app.db.QueryRow(`SELECT u.id, u.username, u.password, u.is_admin,
		u.must_change_password, u.created_at
		FROM sessions s JOIN users u ON u.id = s.user_id
		WHERE s.token_hash = ? AND s.expires_at > ?`,
		sessionTokenHash(cookie.Value), time.Now().UTC().Format(time.RFC3339Nano),
	).Scan(&user.ID, &user.Username, &user.Password, &user.IsAdmin, &user.MustChangePassword, &user.CreatedAt)
	if err != nil {
		return nil, false
	}
	return &user, true
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
	user, ok := app.getSession(r)
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
	feed, _, _, _, err := fetchFeed(url, "", "")
	if err != nil {
		return "Untitled Feed", err
	}
	if feed.Title != "" {
		return feed.Title, nil
	}
	return "Untitled Feed", nil
}

func fetchFeed(rawURL, etag, lastModified string) (*gofeed.Feed, string, string, bool, error) {
	request, err := http.NewRequest(http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, "", "", false, err
	}
	request.Header.Set("Accept", "application/atom+xml,application/rss+xml,application/xml,text/xml,*/*;q=0.8")
	request.Header.Set("User-Agent", "feedss/1.0")
	if etag != "" {
		request.Header.Set("If-None-Match", etag)
	}
	if lastModified != "" {
		request.Header.Set("If-Modified-Since", lastModified)
	}
	response, err := (&http.Client{Timeout: 30 * time.Second}).Do(request)
	if err != nil {
		return nil, "", "", false, err
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusNotModified {
		return nil, etag, lastModified, true, nil
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, "", "", false, fmt.Errorf("feed returned %s", response.Status)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, maxFeedResponseBytes+1))
	if err != nil {
		return nil, "", "", false, err
	}
	if len(data) > maxFeedResponseBytes {
		return nil, "", "", false, errors.New("feed response exceeds 16 MiB")
	}
	parsed, err := gofeed.NewParser().Parse(strings.NewReader(string(data)))
	if err != nil {
		return nil, "", "", false, err
	}
	return parsed, response.Header.Get("ETag"), response.Header.Get("Last-Modified"), false, nil
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
	z := xhtml.NewTokenizer(strings.NewReader(rawHTML))
	anchorHref := ""
	for {
		switch z.Next() {
		case xhtml.ErrorToken:
			return ""
		case xhtml.StartTagToken:
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
		case xhtml.TextToken:
			if anchorHref != "" && strings.Contains(strings.ToLower(string(z.Text())), "comment") {
				return anchorHref
			}
		case xhtml.EndTagToken:
			if z.Token().Data == "a" {
				anchorHref = ""
			}
		}
	}
}

func maintainStoredArticles(db *sql.DB) error {
	cutoff := time.Now().UTC().Add(-articleFreshnessWindow).Format(time.RFC3339)
	if _, err := db.Exec("DELETE FROM articles WHERE is_saved = 0 AND published_at IS NOT NULL AND published_at < ?", cutoff); err != nil {
		return err
	}
	titleRows, err := db.Query("SELECT id, title FROM articles")
	if err != nil {
		return err
	}
	type titleUpdate struct {
		id    int64
		title string
	}
	titleUpdates := make([]titleUpdate, 0)
	for titleRows.Next() {
		var id int64
		var title string
		if err := titleRows.Scan(&id, &title); err != nil {
			titleRows.Close()
			return err
		}
		normalized := normalizeArticleTitle(title)
		if normalized == "" {
			normalized = "Untitled article"
		}
		if normalized != title {
			titleUpdates = append(titleUpdates, titleUpdate{id: id, title: normalized})
		}
	}
	if err := titleRows.Close(); err != nil {
		return err
	}
	for _, update := range titleUpdates {
		if _, err := db.Exec("UPDATE articles SET title = ? WHERE id = ?", update.title, update.id); err != nil {
			return err
		}
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
	if title := normalizeArticleTitle(item.Title); title != "" {
		return title
	}
	text := normalizeArticleTitle(item.Description)
	runes := []rune(text)
	if len(runes) > 100 {
		text = strings.TrimSpace(string(runes[:100])) + "..."
	}
	if text != "" {
		return text
	}
	return "Untitled article"
}

func normalizeArticleTitle(rawTitle string) string {
	decoded := strings.TrimSpace(rawTitle)
	for range 4 {
		next := stdhtml.UnescapeString(decoded)
		if next == decoded {
			break
		}
		decoded = next
	}
	z := xhtml.NewTokenizer(strings.NewReader(decoded))
	var text strings.Builder
	for {
		switch z.Next() {
		case xhtml.ErrorToken:
			return strings.Join(strings.Fields(text.String()), " ")
		case xhtml.TextToken:
			text.Write(z.Text())
		}
	}
}

func (app *App) refreshFeed(userID int64, feedID int64) error {
	err := app.refreshFeedContent(userID, feedID)
	refreshedAt := time.Now().UTC().Format(time.RFC3339)
	if err != nil {
		message := err.Error()
		if len(message) > 2000 {
			message = message[:2000]
		}
		if _, statusErr := app.db.Exec(
			"UPDATE feeds SET last_refresh_error = ?, last_refresh_at = ? WHERE id = ? AND created_by = ?",
			message, refreshedAt, feedID, userID,
		); statusErr != nil {
			log.Printf("record refresh failure for feed %d: %v", feedID, statusErr)
		}
		return err
	}
	if _, statusErr := app.db.Exec(
		"UPDATE feeds SET last_refresh_error = '', last_refresh_at = ?, last_successful_refresh_at = ? WHERE id = ? AND created_by = ?",
		refreshedAt, refreshedAt, feedID, userID,
	); statusErr != nil {
		return fmt.Errorf("record successful refresh: %w", statusErr)
	}
	return nil
}

func (app *App) refreshFeedContent(userID int64, feedID int64) error {
	var feed Feed
	err := app.db.QueryRow(
		"SELECT id, title, url, group_id, display_mode, sort_direction, created_by, created_at, COALESCE(etag, ''), COALESCE(last_modified, '') FROM feeds WHERE id = ? AND created_by = ?",
		feedID,
		userID,
	).Scan(&feed.ID, &feed.Title, &feed.URL, &feed.GroupID, &feed.DisplayMode, &feed.SortDirection, &feed.CreatedBy, &feed.CreatedAt, &feed.ETag, &feed.LastModified)
	if err != nil {
		return err
	}

	parsed, etag, lastModified, notModified, err := fetchFeed(feed.URL, feed.ETag, feed.LastModified)
	if err != nil {
		return err
	}
	if notModified {
		return nil
	}
	if _, err := app.db.Exec("UPDATE feeds SET etag = ?, last_modified = ? WHERE id = ? AND created_by = ?", etag, lastModified, feed.ID, userID); err != nil {
		return err
	}
	if parsed.Title != "" && feed.Title == "" {
		if _, err := app.db.Exec("UPDATE feeds SET title = ? WHERE id = ?", parsed.Title, feed.ID); err != nil {
			log.Printf("update feed title: %v", err)
		}
	}
	cutoff := time.Now().UTC().Add(-articleFreshnessWindow)
	if _, err := app.db.Exec("DELETE FROM articles WHERE feed_id = ? AND is_saved = 0 AND published_at IS NOT NULL AND published_at < ?", feedID, cutoff.Format(time.RFC3339)); err != nil {
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
	if err := app.db.QueryRow("SELECT COUNT(*) FROM articles WHERE feed_id = ? AND is_saved = 0", feedID).Scan(&total); err != nil {
		return err
	}
	if total > settings.MaxArticlesPerFeed {
		deleteCount := total - settings.MaxArticlesPerFeed
		_, err = app.db.Exec(
			"DELETE FROM articles WHERE id IN (SELECT id FROM articles WHERE feed_id = ? AND is_saved = 0 ORDER BY order_index ASC, published_at ASC, id ASC LIMIT ?)",
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
	user, ok := app.getSession(r)
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
	user, ok := app.getSession(r)
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
	user, ok := app.getSession(r)
	if !ok || user == nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}
	feedID := strings.TrimSpace(r.FormValue("feed_id"))
	feedURL := strings.TrimSpace(r.FormValue("url"))
	displayMode := strings.TrimSpace(r.FormValue("display_mode"))
	sortDirection := strings.TrimSpace(r.FormValue("sort_direction"))
	validDisplayModes := map[string]bool{"headline": true, "headline-blurb": true, "full": true}
	if feedURL == "" && feedID != "" {
		if err := app.db.QueryRow("SELECT url FROM feeds WHERE id = ? AND created_by = ?", feedID, user.ID).Scan(&feedURL); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				http.Error(w, "feed not found", http.StatusNotFound)
				return
			}
			http.Error(w, "failed to load feed", http.StatusInternalServerError)
			return
		}
	}
	parsedURL, urlErr := url.Parse(feedURL)
	if feedID == "" || urlErr != nil || parsedURL.Host == "" || (parsedURL.Scheme != "http" && parsedURL.Scheme != "https") || !validDisplayModes[displayMode] || (sortDirection != "asc" && sortDirection != "desc") {
		http.Error(w, "invalid feed settings", http.StatusBadRequest)
		return
	}
	result, err := app.db.Exec(
		"UPDATE feeds SET url = ?, display_mode = ?, sort_direction = ?, etag = CASE WHEN url = ? THEN etag ELSE '' END, last_modified = CASE WHEN url = ? THEN last_modified ELSE '' END WHERE id = ? AND created_by = ?",
		feedURL, displayMode, sortDirection, feedURL, feedURL, feedID, user.ID,
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
	jsonSuccess(w, map[string]string{"status": "ok", "url": feedURL, "display_mode": displayMode, "sort_direction": sortDirection})
}

func (app *App) handleDeleteFeedAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	user, ok := app.getSession(r)
	if !ok || user == nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	feedID := strings.TrimSpace(r.FormValue("feed_id"))
	if feedID == "" {
		http.Error(w, "feed_id is required", http.StatusBadRequest)
		return
	}
	tx, err := app.db.Begin()
	if err != nil {
		http.Error(w, "failed to remove feed", http.StatusInternalServerError)
		return
	}
	defer tx.Rollback()
	if _, err := tx.Exec("DELETE FROM articles WHERE feed_id = ? AND feed_id IN (SELECT id FROM feeds WHERE created_by = ?)", feedID, user.ID); err != nil {
		log.Printf("delete feed articles: %v", err)
		http.Error(w, "failed to remove feed", http.StatusInternalServerError)
		return
	}
	result, err := tx.Exec("DELETE FROM feeds WHERE id = ? AND created_by = ?", feedID, user.ID)
	if err != nil {
		log.Printf("delete feed: %v", err)
		http.Error(w, "failed to remove feed", http.StatusInternalServerError)
		return
	}
	deleted, _ := result.RowsAffected()
	if deleted == 0 {
		http.Error(w, "feed not found", http.StatusNotFound)
		return
	}
	if err := tx.Commit(); err != nil {
		http.Error(w, "failed to remove feed", http.StatusInternalServerError)
		return
	}
	jsonSuccess(w, map[string]string{"status": "ok"})
}

func (app *App) handleArticlesAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	user, ok := app.getSession(r)
	if !ok || user == nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	feedID := r.URL.Query().Get("feed_id")
	groupID := r.URL.Query().Get("group_id")
	savedOnly := r.URL.Query().Get("saved") == "1" || strings.EqualFold(r.URL.Query().Get("saved"), "true")
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
	if feedID == "" && groupID == "" && !savedOnly {
		jsonSuccess(w, ArticlePage{Articles: []Article{}, Total: 0})
		return
	}
	var readThrough *articleCursor
	var err error
	if !savedOnly {
		readThrough, err = app.latestArticleCursor(user.ID, feedID, groupID)
		if err != nil {
			log.Printf("find article read-through boundary: %v", err)
			http.Error(w, "failed to load articles", http.StatusInternalServerError)
			return
		}
	}
	var articles []Article
	var total int
	var hasMore bool
	var cursor *articleCursor
	cursorOrder, cursorOrderErr := strconv.ParseInt(r.URL.Query().Get("cursor_order_index"), 10, 64)
	cursorID, cursorIDErr := strconv.ParseInt(r.URL.Query().Get("cursor_id"), 10, 64)
	if cursorOrderErr == nil && cursorIDErr == nil {
		cursor = &articleCursor{OrderIndex: cursorOrder, ID: cursorID}
	}
	unreadOnly := r.URL.Query().Get("unread_only") == "1" || strings.EqualFold(r.URL.Query().Get("unread_only"), "true")
	if savedOnly {
		articles, total, err = app.listSavedArticlesPage(user.ID, limit, offset)
		hasMore = offset+len(articles) < total
	} else if groupID != "" {
		sortDirection := "desc"
		if settings, settingsErr := app.getSettings(); settingsErr == nil {
			sortDirection = settings.DefaultSortOrder
		}
		if offset > 0 && cursor == nil && !unreadOnly {
			articles, total, err = app.listArticlesForGroupPage(user.ID, groupID, sortDirection, limit, offset)
			hasMore = offset+len(articles) < total
		} else {
			articles, total, hasMore, err = app.listArticlesForGroupCursorPage(user.ID, groupID, sortDirection, limit, cursor, unreadOnly)
		}
	} else {
		var sortDirection string
		err = app.db.QueryRow("SELECT sort_direction FROM feeds WHERE id = ? AND created_by = ?", feedID, user.ID).Scan(&sortDirection)
		if errors.Is(err, sql.ErrNoRows) {
			http.Error(w, "feed not found", http.StatusNotFound)
			return
		}
		if err == nil {
			if offset > 0 && cursor == nil && !unreadOnly {
				articles, total, err = app.listArticlesForFeedPage(user.ID, feedID, sortDirection, limit, offset)
				hasMore = offset+len(articles) < total
			} else {
				articles, total, hasMore, err = app.listArticlesForFeedCursorPage(user.ID, feedID, sortDirection, limit, cursor, unreadOnly)
			}
		}
	}
	if err != nil {
		log.Printf("list articles: %v", err)
		http.Error(w, "failed to load articles", http.StatusInternalServerError)
		return
	}
	page := ArticlePage{Articles: articles, Total: total, HasMore: hasMore}
	if readThrough != nil {
		page.ReadThroughOrderIndex = &readThrough.OrderIndex
		page.ReadThroughID = &readThrough.ID
	}
	jsonSuccess(w, page)
}

func (app *App) latestArticleCursor(userID int64, feedID, groupID string) (*articleCursor, error) {
	query := `SELECT MAX(a.order_index), MAX(a.id)
		FROM articles a
		JOIN feeds f ON f.id = a.feed_id
		WHERE a.feed_id = ? AND f.created_by = ?`
	args := []any{feedID, userID}
	if groupID != "" {
		query = `SELECT MAX(a.order_index), MAX(a.id)
			FROM articles a
			JOIN feeds f ON f.id = a.feed_id
			WHERE f.group_id = ? AND f.created_by = ?`
		args = []any{groupID, userID}
	}
	var orderIndex, articleID sql.NullInt64
	if err := app.db.QueryRow(query, args...).Scan(&orderIndex, &articleID); err != nil {
		return nil, err
	}
	if !orderIndex.Valid || !articleID.Valid {
		return nil, nil
	}
	return &articleCursor{OrderIndex: orderIndex.Int64, ID: articleID.Int64}, nil
}

func (app *App) handleArticleReadAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	user, ok := app.getSession(r)
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
	readThroughOrderText := strings.TrimSpace(r.FormValue("read_through_order_index"))
	readThroughIDText := strings.TrimSpace(r.FormValue("read_through_id"))
	var readThroughOrder, readThroughID int64
	if (readThroughOrderText == "") != (readThroughIDText == "") {
		http.Error(w, "read-through order and article id must be provided together", http.StatusBadRequest)
		return
	}
	if readThroughOrderText != "" {
		var parseErr error
		readThroughOrder, parseErr = strconv.ParseInt(readThroughOrderText, 10, 64)
		if parseErr == nil {
			readThroughID, parseErr = strconv.ParseInt(readThroughIDText, 10, 64)
		}
		if parseErr != nil {
			http.Error(w, "invalid read-through boundary", http.StatusBadRequest)
			return
		}
	}
	var result sql.Result
	var err error
	if articleID != "" {
		result, err = app.db.Exec(
			"UPDATE articles SET is_read = 1 WHERE id = ? AND feed_id IN (SELECT id FROM feeds WHERE created_by = ?)",
			articleID, user.ID,
		)
	} else if feedID != "" {
		query := "UPDATE articles SET is_read = 1 WHERE feed_id = ? AND feed_id IN (SELECT id FROM feeds WHERE created_by = ?)"
		args := []any{feedID, user.ID}
		if readThroughOrderText != "" {
			query += " AND (order_index < ? OR (order_index = ? AND id <= ?)) AND id <= ?"
			args = append(args, readThroughOrder, readThroughOrder, readThroughID, readThroughID)
		}
		result, err = app.db.Exec(query, args...)
	} else if groupID != "" {
		query := "UPDATE articles SET is_read = 1 WHERE feed_id IN (SELECT id FROM feeds WHERE group_id = ? AND created_by = ?)"
		args := []any{groupID, user.ID}
		if readThroughOrderText != "" {
			query += " AND (order_index < ? OR (order_index = ? AND id <= ?)) AND id <= ?"
			args = append(args, readThroughOrder, readThroughOrder, readThroughID, readThroughID)
		}
		result, err = app.db.Exec(query, args...)
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

func (app *App) handleArticleSavedAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	user, ok := app.getSession(r)
	if !ok || user == nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}
	articleID := strings.TrimSpace(r.FormValue("article_id"))
	if articleID == "" {
		http.Error(w, "article_id is required", http.StatusBadRequest)
		return
	}
	saved, err := strconv.ParseBool(strings.TrimSpace(r.FormValue("saved")))
	if err != nil {
		http.Error(w, "saved must be true or false", http.StatusBadRequest)
		return
	}
	result, err := app.db.Exec(
		"UPDATE articles SET is_saved = ? WHERE id = ? AND feed_id IN (SELECT id FROM feeds WHERE created_by = ?)",
		boolToInt(saved), articleID, user.ID,
	)
	if err != nil {
		log.Printf("update saved article: %v", err)
		http.Error(w, "failed to update saved article", http.StatusInternalServerError)
		return
	}
	updated, _ := result.RowsAffected()
	if updated == 0 {
		http.Error(w, "article not found", http.StatusNotFound)
		return
	}
	jsonSuccess(w, map[string]any{"status": "ok", "saved": saved})
}

func (app *App) handleSearchAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	user, ok := app.getSession(r)
	if !ok || user == nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	if query == "" {
		http.Error(w, "search query is required", http.StatusBadRequest)
		return
	}
	if len(query) > 500 {
		http.Error(w, "search query is too long", http.StatusBadRequest)
		return
	}
	feedID := strings.TrimSpace(r.URL.Query().Get("feed_id"))
	groupID := strings.TrimSpace(r.URL.Query().Get("group_id"))
	if feedID != "" && groupID != "" {
		http.Error(w, "choose either feed or group scope", http.StatusBadRequest)
		return
	}
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
	articles, total, err := app.searchArticles(user.ID, query, feedID, groupID, limit, offset)
	if err != nil {
		log.Printf("search articles: %v", err)
		http.Error(w, "failed to search articles", http.StatusInternalServerError)
		return
	}
	jsonSuccess(w, ArticlePage{Articles: articles, Total: total, HasMore: offset+len(articles) < total})
}

const maxProxiedImageBytes = 12 << 20
const maxFaviconPageBytes = 2 << 20
const maxCachedFavicons = 256
const faviconCacheTTL = 24 * time.Hour

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

func fetchRemoteImage(ctx context.Context, rawURL string) ([]byte, string, error) {
	remoteURL, err := parseRemoteImageURL(rawURL)
	if err != nil {
		return nil, "", err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, remoteURL.String(), nil)
	if err != nil {
		return nil, "", err
	}
	request.Header.Set("Accept", "image/avif,image/webp,image/png,image/jpeg,image/gif,image/*;q=0.8")
	request.Header.Set("User-Agent", "feedss/1.0")
	response, err := imageProxyClient().Do(request)
	if err != nil {
		log.Printf("image proxy %s: %v", remoteURL.Hostname(), err)
		return nil, "", err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, "", fmt.Errorf("image host returned %s", response.Status)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, maxProxiedImageBytes+1))
	if err != nil {
		return nil, "", err
	}
	if len(data) > maxProxiedImageBytes {
		return nil, "", errors.New("image is too large")
	}
	contentType := response.Header.Get("Content-Type")
	if !strings.HasPrefix(strings.ToLower(contentType), "image/") {
		contentType = http.DetectContentType(data)
	}
	if !strings.HasPrefix(strings.ToLower(contentType), "image/") {
		return nil, "", errors.New("remote content is not an image")
	}
	return data, contentType, nil
}

func writeProxiedImage(w http.ResponseWriter, data []byte, contentType string) {
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Cache-Control", "private, max-age=86400")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

func proxyRemoteImage(w http.ResponseWriter, r *http.Request, rawURL string) {
	if _, err := parseRemoteImageURL(rawURL); err != nil {
		http.Error(w, "invalid image URL", http.StatusBadRequest)
		return
	}
	data, contentType, err := fetchRemoteImage(r.Context(), rawURL)
	if err != nil {
		http.Error(w, "image unavailable", http.StatusBadGateway)
		return
	}
	writeProxiedImage(w, data, contentType)
}

func (app *App) handleImageProxy(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	proxyRemoteImage(w, r, r.URL.Query().Get("url"))
}

func discoverFaviconURL(siteURL *url.URL, document io.Reader) string {
	root, err := xhtml.Parse(io.LimitReader(document, maxFaviconPageBytes))
	if err != nil {
		return ""
	}
	var fallback string
	var visit func(*xhtml.Node) string
	visit = func(node *xhtml.Node) string {
		if node.Type == xhtml.ElementNode && node.Data == "link" {
			var href string
			var relTokens []string
			for _, attribute := range node.Attr {
				switch strings.ToLower(attribute.Key) {
				case "href":
					href = strings.TrimSpace(attribute.Val)
				case "rel":
					relTokens = strings.Fields(strings.ToLower(attribute.Val))
				}
			}
			if href != "" {
				for _, token := range relTokens {
					if token != "icon" && token != "shortcut" && token != "apple-touch-icon" {
						continue
					}
					candidate, err := siteURL.Parse(href)
					if err != nil {
						continue
					}
					if _, err := parseRemoteImageURL(candidate.String()); err != nil {
						continue
					}
					if token == "icon" {
						return candidate.String()
					}
					if fallback == "" {
						fallback = candidate.String()
					}
				}
			}
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			if found := visit(child); found != "" {
				return found
			}
		}
		return ""
	}
	if faviconURL := visit(root); faviconURL != "" {
		return faviconURL
	}
	return fallback
}

func (app *App) loadCachedFavicon(key string) (cachedFavicon, bool) {
	app.faviconMu.RLock()
	cached, ok := app.faviconCache[key]
	app.faviconMu.RUnlock()
	if !ok || time.Now().After(cached.expiresAt) {
		if ok {
			app.faviconMu.Lock()
			if current, exists := app.faviconCache[key]; exists && time.Now().After(current.expiresAt) {
				delete(app.faviconCache, key)
			}
			app.faviconMu.Unlock()
		}
		return cachedFavicon{}, false
	}
	return cached, true
}

func (app *App) storeCachedFavicon(key string, favicon cachedFavicon) {
	app.faviconMu.Lock()
	defer app.faviconMu.Unlock()
	if app.faviconCache == nil {
		app.faviconCache = make(map[string]cachedFavicon)
	}
	_, replacing := app.faviconCache[key]
	if !replacing && len(app.faviconCache) >= maxCachedFavicons {
		var oldestKey string
		var oldestExpiry time.Time
		for existingKey, existing := range app.faviconCache {
			if oldestKey == "" || existing.expiresAt.Before(oldestExpiry) {
				oldestKey = existingKey
				oldestExpiry = existing.expiresAt
			}
		}
		delete(app.faviconCache, oldestKey)
	}
	app.faviconCache[key] = favicon
}

func (app *App) handleFaviconProxy(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	siteURL, err := parseRemoteImageURL(r.URL.Query().Get("url"))
	if err != nil {
		http.Error(w, "invalid site URL", http.StatusBadRequest)
		return
	}
	siteURL.Path = "/"
	siteURL.RawPath = ""
	siteURL.RawQuery = ""
	siteURL.Fragment = ""
	cacheKey := siteURL.String()
	if cached, ok := app.loadCachedFavicon(cacheKey); ok {
		writeProxiedImage(w, cached.data, cached.contentType)
		return
	}
	var faviconURL string
	request, err := http.NewRequestWithContext(r.Context(), http.MethodGet, siteURL.String(), nil)
	if err == nil {
		request.Header.Set("Accept", "text/html,application/xhtml+xml")
		request.Header.Set("User-Agent", "feedss/1.0")
		response, requestErr := imageProxyClient().Do(request)
		if requestErr == nil {
			defer response.Body.Close()
			if response.StatusCode >= 200 && response.StatusCode < 300 {
				faviconURL = discoverFaviconURL(siteURL, response.Body)
			}
		}
	}
	if faviconURL == "" {
		fallback := *siteURL
		fallback.Path = "/favicon.ico"
		faviconURL = fallback.String()
	}
	data, contentType, err := fetchRemoteImage(r.Context(), faviconURL)
	if err != nil {
		http.Error(w, "favicon unavailable", http.StatusBadGateway)
		return
	}
	app.storeCachedFavicon(cacheKey, cachedFavicon{
		data: data, contentType: contentType, expiresAt: time.Now().Add(faviconCacheTTL),
	})
	writeProxiedImage(w, data, contentType)
}

func (app *App) handleRefreshAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	user, ok := app.getSession(r)
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
	requestedFeedID := strings.TrimSpace(r.FormValue("feed_id"))
	targets := make([]feedRefreshTarget, 0, len(feeds))
	for _, feed := range feeds {
		if requestedFeedID != "" && strconv.FormatInt(feed.ID, 10) != requestedFeedID {
			continue
		}
		targets = append(targets, feedRefreshTarget{ID: feed.ID, UserID: user.ID})
	}
	if requestedFeedID != "" && len(targets) == 0 {
		http.Error(w, "feed not found", http.StatusNotFound)
		return
	}
	refreshed, failed := app.refreshFeeds(targets)
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
			COALESCE(SUM(CASE WHEN a.is_read = 0 THEN 1 ELSE 0 END), 0), COALESCE(f.last_refresh_error, ''),
			COALESCE(f.last_refresh_at, ''), COALESCE(f.last_successful_refresh_at, '')
		FROM feeds f
		LEFT JOIN articles a ON a.feed_id = f.id
		WHERE f.created_by = ?
		GROUP BY f.id, f.title, f.url, f.group_id, f.display_mode, f.sort_direction, f.created_by, f.created_at,
			f.last_refresh_error, f.last_refresh_at, f.last_successful_refresh_at
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
		if err := rows.Scan(
			&f.ID, &f.Title, &f.URL, &f.GroupID, &f.DisplayMode, &f.SortDirection, &f.CreatedBy,
			&f.CreatedAt, &f.UnreadCount, &f.LastRefreshError, &f.LastRefreshAt, &f.LastSuccessfulRefreshAt,
		); err != nil {
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
		"SELECT a.id, a.feed_id, f.title, a.title, COALESCE(a.link, ''), COALESCE(a.comments_link, ''), COALESCE(a.description, ''), COALESCE(a.content, ''), a.published_at, COALESCE(a.guid, ''), COALESCE(a.media_url, ''), a.order_index, a.is_read, a.is_saved FROM articles a JOIN feeds f ON f.id = a.feed_id WHERE a.feed_id = ? AND f.created_by = ? ORDER BY a.order_index "+direction+", a.id "+direction+" LIMIT ? OFFSET ?",
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
		if err := rows.Scan(&a.ID, &a.FeedID, &a.FeedTitle, &a.Title, &a.Link, &a.CommentsLink, &a.Description, &a.Content, &publishedAt, &a.GUID, &a.MediaURL, &a.OrderIndex, &a.IsRead, &a.IsSaved); err != nil {
			return nil, 0, err
		}
		if publishedAt.Valid {
			a.PublishedAt, _ = time.Parse(time.RFC3339, publishedAt.String)
		}
		a.Title = normalizeArticleTitle(a.Title)
		articles = append(articles, a)
	}
	return articles, total, rows.Err()
}

func (app *App) listArticlesForFeedCursorPage(userID int64, feedID, sortDirection string, limit int, cursor *articleCursor, unreadOnly bool) ([]Article, int, bool, error) {
	direction := "DESC"
	comparison := "<"
	if strings.EqualFold(sortDirection, "asc") {
		direction = "ASC"
		comparison = ">"
	}
	where := "a.feed_id = ? AND f.created_by = ?"
	args := []any{feedID, userID}
	if unreadOnly {
		where += " AND a.is_read = 0"
	}
	var total int
	if err := app.db.QueryRow("SELECT COUNT(*) FROM articles a JOIN feeds f ON f.id = a.feed_id WHERE "+where, args...).Scan(&total); err != nil {
		return nil, 0, false, err
	}
	if cursor != nil {
		where += " AND (a.order_index " + comparison + " ? OR (a.order_index = ? AND a.id " + comparison + " ?))"
		args = append(args, cursor.OrderIndex, cursor.OrderIndex, cursor.ID)
	}
	args = append(args, limit+1)
	rows, err := app.db.Query(
		"SELECT a.id, a.feed_id, f.title, a.title, COALESCE(a.link, ''), COALESCE(a.comments_link, ''), COALESCE(a.description, ''), COALESCE(a.content, ''), a.published_at, COALESCE(a.guid, ''), COALESCE(a.media_url, ''), a.order_index, a.is_read, a.is_saved FROM articles a JOIN feeds f ON f.id = a.feed_id WHERE "+where+" ORDER BY a.order_index "+direction+", a.id "+direction+" LIMIT ?",
		args...,
	)
	if err != nil {
		return nil, 0, false, err
	}
	defer rows.Close()
	articles := make([]Article, 0, limit+1)
	for rows.Next() {
		var article Article
		var publishedAt sql.NullString
		if err := rows.Scan(&article.ID, &article.FeedID, &article.FeedTitle, &article.Title, &article.Link, &article.CommentsLink, &article.Description, &article.Content, &publishedAt, &article.GUID, &article.MediaURL, &article.OrderIndex, &article.IsRead, &article.IsSaved); err != nil {
			return nil, 0, false, err
		}
		if publishedAt.Valid {
			article.PublishedAt, _ = time.Parse(time.RFC3339, publishedAt.String)
		}
		article.Title = normalizeArticleTitle(article.Title)
		articles = append(articles, article)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, false, err
	}
	hasMore := len(articles) > limit
	if hasMore {
		articles = articles[:limit]
	}
	return articles, total, hasMore, nil
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
			COALESCE(a.media_url, ''), a.order_index, a.is_read, a.is_saved
		FROM articles a
		JOIN feeds f ON f.id = a.feed_id
		WHERE f.group_id = ? AND f.created_by = ?
		ORDER BY a.order_index `+direction+`, a.id `+direction+`
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
			&article.GUID, &article.MediaURL, &article.OrderIndex, &article.IsRead, &article.IsSaved,
		); err != nil {
			return nil, 0, err
		}
		if publishedAt.Valid {
			article.PublishedAt, _ = time.Parse(time.RFC3339, publishedAt.String)
		}
		article.Title = normalizeArticleTitle(article.Title)
		articles = append(articles, article)
	}
	return articles, total, rows.Err()
}

func (app *App) listArticlesForGroupCursorPage(userID int64, groupID, sortDirection string, limit int, cursor *articleCursor, unreadOnly bool) ([]Article, int, bool, error) {
	direction := "DESC"
	comparison := "<"
	if strings.EqualFold(sortDirection, "asc") {
		direction = "ASC"
		comparison = ">"
	}
	where := "f.group_id = ? AND f.created_by = ?"
	args := []any{groupID, userID}
	if unreadOnly {
		where += " AND a.is_read = 0"
	}
	var total int
	if err := app.db.QueryRow("SELECT COUNT(*) FROM articles a JOIN feeds f ON f.id = a.feed_id WHERE "+where, args...).Scan(&total); err != nil {
		return nil, 0, false, err
	}
	if cursor != nil {
		where += " AND (a.order_index " + comparison + " ? OR (a.order_index = ? AND a.id " + comparison + " ?))"
		args = append(args, cursor.OrderIndex, cursor.OrderIndex, cursor.ID)
	}
	args = append(args, limit+1)
	rows, err := app.db.Query(
		`SELECT a.id, a.feed_id, f.title, a.title, COALESCE(a.link, ''), COALESCE(a.comments_link, ''),
			COALESCE(a.description, ''), COALESCE(a.content, ''), a.published_at, COALESCE(a.guid, ''),
			COALESCE(a.media_url, ''), a.order_index, a.is_read, a.is_saved
		FROM articles a
		JOIN feeds f ON f.id = a.feed_id
		WHERE `+where+`
		ORDER BY a.order_index `+direction+`, a.id `+direction+`
		LIMIT ?`,
		args...,
	)
	if err != nil {
		return nil, 0, false, err
	}
	defer rows.Close()
	articles := make([]Article, 0, limit+1)
	for rows.Next() {
		var article Article
		var publishedAt sql.NullString
		if err := rows.Scan(&article.ID, &article.FeedID, &article.FeedTitle, &article.Title, &article.Link, &article.CommentsLink, &article.Description, &article.Content, &publishedAt, &article.GUID, &article.MediaURL, &article.OrderIndex, &article.IsRead, &article.IsSaved); err != nil {
			return nil, 0, false, err
		}
		if publishedAt.Valid {
			article.PublishedAt, _ = time.Parse(time.RFC3339, publishedAt.String)
		}
		article.Title = normalizeArticleTitle(article.Title)
		articles = append(articles, article)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, false, err
	}
	hasMore := len(articles) > limit
	if hasMore {
		articles = articles[:limit]
	}
	return articles, total, hasMore, nil
}

func (app *App) listSavedArticlesPage(userID int64, limit, offset int) ([]Article, int, error) {
	const where = "a.is_saved = 1 AND f.created_by = ?"
	var total int
	if err := app.db.QueryRow(
		"SELECT COUNT(*) FROM articles a JOIN feeds f ON f.id = a.feed_id WHERE "+where,
		userID,
	).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := app.db.Query(
		`SELECT a.id, a.feed_id, f.title, a.title, COALESCE(a.link, ''), COALESCE(a.comments_link, ''),
			COALESCE(a.description, ''), COALESCE(a.content, ''), a.published_at, COALESCE(a.guid, ''),
			COALESCE(a.media_url, ''), a.order_index, a.is_read, a.is_saved
		FROM articles a
		JOIN feeds f ON f.id = a.feed_id
		WHERE `+where+`
		ORDER BY a.order_index DESC, a.id DESC
		LIMIT ? OFFSET ?`,
		userID, limit, offset,
	)
	if err != nil {
		return nil, 0, err
	}
	articles, err := scanArticles(rows)
	return articles, total, err
}

func ftsSearchQuery(raw string) string {
	tokens := make([]string, 0, 8)
	var token strings.Builder
	flush := func() {
		if token.Len() == 0 {
			return
		}
		tokens = append(tokens, `"`+token.String()+`"*`)
		token.Reset()
	}
	for _, r := range raw {
		if unicode.IsLetter(r) || unicode.IsNumber(r) {
			token.WriteRune(unicode.ToLower(r))
		} else {
			flush()
		}
	}
	flush()
	return strings.Join(tokens, " AND ")
}

func (app *App) searchArticles(userID int64, rawQuery, feedID, groupID string, limit, offset int) ([]Article, int, error) {
	match := ftsSearchQuery(rawQuery)
	if match == "" {
		return []Article{}, 0, nil
	}
	where := "article_search MATCH ? AND f.created_by = ?"
	args := []any{match, userID}
	if feedID != "" {
		where += " AND a.feed_id = ?"
		args = append(args, feedID)
	} else if groupID != "" {
		where += " AND f.group_id = ?"
		args = append(args, groupID)
	}
	var total int
	if err := app.db.QueryRow(
		`SELECT COUNT(*) FROM article_search
		JOIN articles a ON a.id = article_search.rowid
		JOIN feeds f ON f.id = a.feed_id
		WHERE `+where,
		args...,
	).Scan(&total); err != nil {
		return nil, 0, err
	}
	queryArgs := append(append([]any{}, args...), limit, offset)
	rows, err := app.db.Query(
		`SELECT a.id, a.feed_id, f.title, a.title, COALESCE(a.link, ''), COALESCE(a.comments_link, ''),
			COALESCE(a.description, ''), COALESCE(a.content, ''), a.published_at, COALESCE(a.guid, ''),
			COALESCE(a.media_url, ''), a.order_index, a.is_read, a.is_saved
		FROM article_search
		JOIN articles a ON a.id = article_search.rowid
		JOIN feeds f ON f.id = a.feed_id
		WHERE `+where+`
		ORDER BY bm25(article_search, 8.0, 2.0, 1.0), a.order_index DESC, a.id DESC
		LIMIT ? OFFSET ?`,
		queryArgs...,
	)
	if err != nil {
		return nil, 0, err
	}
	articles, err := scanArticles(rows)
	return articles, total, err
}

func scanArticles(rows *sql.Rows) ([]Article, error) {
	defer rows.Close()
	articles := make([]Article, 0)
	for rows.Next() {
		var article Article
		var publishedAt sql.NullString
		if err := rows.Scan(
			&article.ID, &article.FeedID, &article.FeedTitle, &article.Title, &article.Link,
			&article.CommentsLink, &article.Description, &article.Content, &publishedAt,
			&article.GUID, &article.MediaURL, &article.OrderIndex, &article.IsRead, &article.IsSaved,
		); err != nil {
			return nil, err
		}
		if publishedAt.Valid {
			article.PublishedAt, _ = time.Parse(time.RFC3339, publishedAt.String)
		}
		article.Title = normalizeArticleTitle(article.Title)
		articles = append(articles, article)
	}
	return articles, rows.Err()
}

func (app *App) handleSettingsAPI(w http.ResponseWriter, r *http.Request) {
	user, ok := app.getSession(r)
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
			settings = defaultAppSettings()
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
		ReleaseCheckEnabled: strings.EqualFold(strings.TrimSpace(r.FormValue("release_check_enabled")), "true") ||
			r.FormValue("release_check_enabled") == "1",
		ReleaseCheckIncludePrereleases: strings.EqualFold(strings.TrimSpace(r.FormValue("release_check_include_prereleases")), "true") ||
			r.FormValue("release_check_include_prereleases") == "1",
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

func (app *App) handleUsersAPI(w http.ResponseWriter, r *http.Request) {
	session, ok := app.getSession(r)
	if !ok || session == nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	admin, err := app.userByID(session.ID)
	if err != nil || !admin.IsAdmin {
		http.Error(w, "admin required", http.StatusForbidden)
		return
	}
	if r.Method == http.MethodGet {
		rows, err := app.db.Query("SELECT id, username, is_admin, must_change_password, created_at FROM users ORDER BY username COLLATE NOCASE")
		if err != nil {
			http.Error(w, "failed to load users", http.StatusInternalServerError)
			return
		}
		defer rows.Close()
		users := make([]User, 0)
		for rows.Next() {
			var user User
			if err := rows.Scan(&user.ID, &user.Username, &user.IsAdmin, &user.MustChangePassword, &user.CreatedAt); err != nil {
				http.Error(w, "failed to load users", http.StatusInternalServerError)
				return
			}
			users = append(users, user)
		}
		if err := rows.Err(); err != nil {
			http.Error(w, "failed to load users", http.StatusInternalServerError)
			return
		}
		jsonSuccess(w, users)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	username := strings.TrimSpace(r.FormValue("username"))
	temporaryPassword := r.FormValue("temporary_password")
	if username == "" || temporaryPassword == "" {
		http.Error(w, "username and temporary password are required", http.StatusBadRequest)
		return
	}
	if len([]byte(temporaryPassword)) > 72 {
		http.Error(w, "temporary password must be 72 bytes or fewer", http.StatusBadRequest)
		return
	}
	passwordHash, err := hashPassword(temporaryPassword)
	if err != nil {
		http.Error(w, "failed to create user", http.StatusInternalServerError)
		return
	}
	tx, err := app.db.Begin()
	if err != nil {
		http.Error(w, "failed to create user", http.StatusInternalServerError)
		return
	}
	defer tx.Rollback()
	result, err := tx.Exec(
		"INSERT INTO users(username, password, is_admin, must_change_password) VALUES(?, ?, 0, 1)",
		username, passwordHash,
	)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			http.Error(w, "username already exists", http.StatusConflict)
			return
		}
		http.Error(w, "failed to create user", http.StatusInternalServerError)
		return
	}
	userID, err := result.LastInsertId()
	if err != nil {
		http.Error(w, "failed to create user", http.StatusInternalServerError)
		return
	}
	if _, err := tx.Exec("INSERT INTO groups(name, created_by) VALUES('Inbox', ?)", userID); err != nil {
		http.Error(w, "failed to create user", http.StatusInternalServerError)
		return
	}
	if err := tx.Commit(); err != nil {
		http.Error(w, "failed to create user", http.StatusInternalServerError)
		return
	}
	jsonSuccess(w, User{ID: userID, Username: username, MustChangePassword: true})
}

func defaultAppSettings() *AppSettings {
	return &AppSettings{
		RefreshIntervalMin:             15,
		MaxArticlesPerFeed:             500,
		DefaultDisplayMode:             "headline",
		DefaultSortOrder:               "desc",
		AutoRefreshEnabled:             true,
		ReleaseCheckEnabled:            true,
		ReleaseCheckIncludePrereleases: false,
	}
}

func (app *App) handleReleaseCheckAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	user, ok := app.getSession(r)
	if !ok || user == nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	settings, err := app.getSettings()
	if err != nil {
		log.Printf("release check settings: %v", err)
		settings = defaultAppSettings()
	}
	result := ReleaseCheckResult{
		Enabled:        settings.ReleaseCheckEnabled,
		CurrentVersion: version,
		ReleasesURL:    githubProjectReleasesURL,
	}
	if !settings.ReleaseCheckEnabled || !isReleaseVersion(version) {
		jsonSuccess(w, result)
		return
	}
	release, err := latestGitHubRelease(r.Context(), settings.ReleaseCheckIncludePrereleases)
	if err != nil {
		log.Printf("release check: %v", err)
		jsonSuccess(w, result)
		return
	}
	result.Release = release
	result.UpdateAvailable = compareReleaseVersions(release.TagName, version) > 0
	jsonSuccess(w, result)
}

func latestGitHubRelease(ctx context.Context, includePrereleases bool) (*GitHubRelease, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, githubReleasesURL, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("User-Agent", "feedss/"+version)
	client := &http.Client{Timeout: 10 * time.Second}
	response, err := client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("github releases returned %s", response.Status)
	}
	var releases []GitHubRelease
	if err := json.NewDecoder(io.LimitReader(response.Body, 2<<20)).Decode(&releases); err != nil {
		return nil, err
	}
	for _, release := range releases {
		if release.Draft || release.TagName == "" {
			continue
		}
		if release.Prerelease && !includePrereleases {
			continue
		}
		return &release, nil
	}
	return nil, errors.New("no matching GitHub release found")
}

func isReleaseVersion(raw string) bool {
	_, ok := parseReleaseVersion(raw)
	return ok
}

func compareReleaseVersions(left, right string) int {
	leftParts, leftOK := parseReleaseVersion(left)
	rightParts, rightOK := parseReleaseVersion(right)
	if !leftOK || !rightOK {
		return 0
	}
	for i := 0; i < len(leftParts); i++ {
		if leftParts[i] > rightParts[i] {
			return 1
		}
		if leftParts[i] < rightParts[i] {
			return -1
		}
	}
	return 0
}

func parseReleaseVersion(raw string) ([3]int, bool) {
	var parts [3]int
	trimmed := strings.TrimPrefix(strings.TrimSpace(raw), "v")
	if trimmed == "" || strings.EqualFold(trimmed, "dev") {
		return parts, false
	}
	if index := strings.IndexAny(trimmed, "-+"); index >= 0 {
		trimmed = trimmed[:index]
	}
	segments := strings.Split(trimmed, ".")
	if len(segments) == 0 || len(segments) > len(parts) {
		return parts, false
	}
	for index, segment := range segments {
		value, err := strconv.Atoi(segment)
		if err != nil || value < 0 {
			return parts, false
		}
		parts[index] = value
	}
	return parts, true
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
	user, ok := app.getSession(r)
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
	user, ok := app.getSession(r)
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
