package main

import (
	"database/sql"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mmcdole/gofeed"
	_ "modernc.org/sqlite"
)

//go:embed templates/*
var templateFS embed.FS

//go:embed static/*
var staticFS embed.FS

const defaultPort = "4317"

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
	CreatedAt time.Time
}

// FeedGroup groups feeds in the UI.
type FeedGroup struct {
	ID         int64
	Name       string
	CreatedBy  int64
	CreatedAt  time.Time
	FeedCount  int
	Selected   bool
}

// Feed defines a feed source.
type Feed struct {
	ID            int64
	Title         string
	URL           string
	GroupID       int64
	DisplayMode   string
	SortDirection string
	CreatedBy     int64
	CreatedAt     time.Time
	Selected      bool
}

// Article describes a feed item.
type Article struct {
	ID               int64
	FeedID           int64
	FeedTitle        string
	Title            string
	Link             string
	CommentsLink     string
	Description      string
	Content          string
	PublishedAt      time.Time
	GUID             string
	MediaURL         string
	OrderIndex      int
}

// App stores runtime state.
type App struct {
	db     *sql.DB
	config AppConfig
	tmpl   *template.Template
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

	mux := http.NewServeMux()
	mux.HandleFunc("/", app.handleIndex)
	mux.HandleFunc("/login", app.handleLogin)
	mux.HandleFunc("/logout", app.handleLogout)
	mux.HandleFunc("/feed/add", app.handleAddFeed)
	mux.HandleFunc("/api/groups", app.handleGroupsAPI)
	mux.HandleFunc("/api/feeds", app.handleFeedsAPI)
	mux.HandleFunc("/api/articles", app.handleArticlesAPI)
	mux.HandleFunc("/api/import-opml", app.handleImportOPML)
	mux.HandleFunc("/api/export-opml", app.handleExportOPML)
	mux.Handle("/static/", http.FileServer(http.FS(staticFS)))

	log.Printf("Starting feedss on :%s", cfg.Port)
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

	if err := initSchema(db); err != nil {
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
			created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
		);`,
		`CREATE TABLE IF NOT EXISTS groups (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL,
			created_by INTEGER NOT NULL,
			created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
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
			created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
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
			FOREIGN KEY(feed_id) REFERENCES feeds(id)
		);`,
		`CREATE INDEX IF NOT EXISTS idx_articles_feed_id ON articles(feed_id);`,
		`CREATE INDEX IF NOT EXISTS idx_articles_published_at ON articles(published_at);`,
	}

	for _, q := range queries {
		if _, err := db.Exec(q); err != nil {
			return fmt.Errorf("schema init: %w", err)
		}
	}
	return nil
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
	return nil
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
		if r.URL.Path == "/login" || r.URL.Path == "/logout" {
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
	if b { return 1 }
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
	groupID := int64(1)
	if groupName := strings.TrimSpace(r.FormValue("group")); groupName != "" {
		groupID = app.ensureGroup(user.ID, groupName)
	}
	feedTitle, _ := app.fetchFeedTitle(url)
	feedDisplay := strings.TrimSpace(r.FormValue("display_mode"))
	if feedDisplay == "" {
		feedDisplay = "headline"
	}
	feedOrder := strings.TrimSpace(r.FormValue("sort_direction"))
	if feedOrder == "" {
		feedOrder = "desc"
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
	if feedID == "" {
		jsonSuccess(w, []Article{})
		return
	}
	articles, err := app.listArticlesForFeed(user.ID, feedID)
	if err != nil {
		log.Printf("list articles: %v", err)
		http.Error(w, "failed to load articles", http.StatusInternalServerError)
		return
	}
	jsonSuccess(w, articles)
}

func (app *App) listGroups(userID int64) ([]FeedGroup, error) {
	rows, err := app.db.Query(
		"SELECT id, name, created_by, created_at FROM groups WHERE created_by = ? ORDER BY name ASC",
		userID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	groups := make([]FeedGroup, 0)
	for rows.Next() {
		var g FeedGroup
		if err := rows.Scan(&g.ID, &g.Name, &g.CreatedBy, &g.CreatedAt); err != nil {
			return nil, err
		}
		if err := app.db.QueryRow("SELECT COUNT(*) FROM feeds WHERE group_id = ?", g.ID).Scan(&g.FeedCount); err != nil {
			return nil, err
		}
		groups = append(groups, g)
	}
	return groups, rows.Err()
}

func (app *App) listFeeds(userID int64) ([]Feed, error) {
	rows, err := app.db.Query(
		"SELECT id, title, url, group_id, display_mode, sort_direction, created_by, created_at FROM feeds WHERE created_by = ? ORDER BY title ASC",
		userID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	feeds := make([]Feed, 0)
	for rows.Next() {
		var f Feed
		if err := rows.Scan(&f.ID, &f.Title, &f.URL, &f.GroupID, &f.DisplayMode, &f.SortDirection, &f.CreatedBy, &f.CreatedAt); err != nil {
			return nil, err
		}
		feeds = append(feeds, f)
	}
	return feeds, rows.Err()
}

func (app *App) listArticlesForFeed(userID int64, feedID string) ([]Article, error) {
	rows, err := app.db.Query(
		"SELECT a.id, a.feed_id, f.title, a.title, a.link, a.comments_link, a.description, a.content, a.published_at, a.guid, a.media_url, a.order_index FROM articles a JOIN feeds f ON f.id = a.feed_id WHERE a.feed_id = ? AND f.created_by = ? ORDER BY a.order_index DESC, a.published_at DESC",
		feedID,
		userID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	articles := make([]Article, 0)
	for rows.Next() {
		var a Article
		var publishedAt sql.NullString
		if err := rows.Scan(&a.ID, &a.FeedID, &a.FeedTitle, &a.Title, &a.Link, &a.CommentsLink, &a.Description, &a.Content, &publishedAt, &a.GUID, &a.MediaURL, &a.OrderIndex); err != nil {
			return nil, err
		}
		if publishedAt.Valid {
			a.PublishedAt, _ = time.Parse(time.RFC3339, publishedAt.String)
		}
		articles = append(articles, a)
	}
	return articles, rows.Err()
}

func (app *App) handleImportOPML(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	jsonSuccess(w, map[string]string{"status": "ok", "message": "OPML import is ready for implementation"})
}

func (app *App) handleExportOPML(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	jsonSuccess(w, map[string]string{"status": "ok", "message": "OPML export is ready for implementation"})
}

func jsonSuccess(w http.ResponseWriter, payload any) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		log.Printf("json encode: %v", err)
	}
}

