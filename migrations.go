package main

import (
	"database/sql"
	"fmt"
	"time"
)

type migration struct {
	Version int
	Name    string
	Apply   func(*sql.DB) error
}

var migrations = []migration{
	{
		Version: 1,
		Name:    "add_article_read_state",
		Apply: func(db *sql.DB) error {
			return ensureColumn(db, "articles", "is_read", "INTEGER NOT NULL DEFAULT 0")
		},
	},
	{
		Version: 2,
		Name:    "add_release_check_settings",
		Apply: func(db *sql.DB) error {
			if err := ensureColumn(db, "app_settings", "release_check_enabled", "INTEGER NOT NULL DEFAULT 1"); err != nil {
				return err
			}
			return ensureColumn(db, "app_settings", "release_check_include_prereleases", "INTEGER NOT NULL DEFAULT 0")
		},
	},
	{
		Version: 3,
		Name:    "add_feed_refresh_status",
		Apply: func(db *sql.DB) error {
			for _, column := range []struct {
				name       string
				definition string
			}{
				{name: "last_refresh_error", definition: "TEXT NOT NULL DEFAULT ''"},
				{name: "last_refresh_at", definition: "TEXT"},
				{name: "last_successful_refresh_at", definition: "TEXT"},
			} {
				if err := ensureColumn(db, "feeds", column.name, column.definition); err != nil {
					return err
				}
			}
			return nil
		},
	},
	{
		Version: 4,
		Name:    "add_temporary_password_state",
		Apply: func(db *sql.DB) error {
			return ensureColumn(db, "users", "must_change_password", "INTEGER NOT NULL DEFAULT 0")
		},
	},
	{
		Version: 5,
		Name:    "add_feed_http_validators",
		Apply: func(db *sql.DB) error {
			if err := ensureColumn(db, "feeds", "etag", "TEXT NOT NULL DEFAULT ''"); err != nil {
				return err
			}
			return ensureColumn(db, "feeds", "last_modified", "TEXT NOT NULL DEFAULT ''")
		},
	},
	{
		Version: 6,
		Name:    "add_saved_articles",
		Apply: func(db *sql.DB) error {
			if err := ensureColumn(db, "articles", "is_saved", "INTEGER NOT NULL DEFAULT 0"); err != nil {
				return err
			}
			_, err := db.Exec("CREATE INDEX IF NOT EXISTS idx_articles_saved ON articles(is_saved, order_index DESC, id DESC)")
			return err
		},
	},
	{
		Version: 7,
		Name:    "add_article_full_text_search",
		Apply: func(db *sql.DB) error {
			statements := []string{
				`CREATE VIRTUAL TABLE IF NOT EXISTS article_search USING fts5(
					title, description, content,
					content='articles', content_rowid='id',
					tokenize='unicode61 remove_diacritics 2'
				)`,
				`CREATE TRIGGER IF NOT EXISTS articles_search_insert AFTER INSERT ON articles BEGIN
					INSERT INTO article_search(rowid, title, description, content)
					VALUES (new.id, new.title, COALESCE(new.description, ''), COALESCE(new.content, ''));
				END`,
				`CREATE TRIGGER IF NOT EXISTS articles_search_delete AFTER DELETE ON articles BEGIN
					INSERT INTO article_search(article_search, rowid, title, description, content)
					VALUES ('delete', old.id, old.title, COALESCE(old.description, ''), COALESCE(old.content, ''));
				END`,
				`CREATE TRIGGER IF NOT EXISTS articles_search_update AFTER UPDATE OF title, description, content ON articles BEGIN
					INSERT INTO article_search(article_search, rowid, title, description, content)
					VALUES ('delete', old.id, old.title, COALESCE(old.description, ''), COALESCE(old.content, ''));
					INSERT INTO article_search(rowid, title, description, content)
					VALUES (new.id, new.title, COALESCE(new.description, ''), COALESCE(new.content, ''));
				END`,
				`INSERT INTO article_search(article_search) VALUES('rebuild')`,
			}
			for _, statement := range statements {
				if _, err := db.Exec(statement); err != nil {
					return err
				}
			}
			return nil
		},
	},
	{
		Version: 8,
		Name:    "add_article_read_timestamp",
		Apply: func(db *sql.DB) error {
			if err := ensureColumn(db, "articles", "read_at", "TEXT"); err != nil {
				return err
			}
			_, err := db.Exec("CREATE INDEX IF NOT EXISTS idx_articles_read_at ON articles(read_at DESC, id DESC)")
			return err
		},
	},
}

func runMigrations(db *sql.DB) error {
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (
		version INTEGER PRIMARY KEY,
		name TEXT NOT NULL,
		applied_at TEXT NOT NULL
	);`); err != nil {
		return fmt.Errorf("create schema migrations table: %w", err)
	}
	for _, migration := range migrations {
		applied, err := migrationApplied(db, migration.Version)
		if err != nil {
			return err
		}
		if applied {
			continue
		}
		if err := migration.Apply(db); err != nil {
			return fmt.Errorf("migration %03d_%s: %w", migration.Version, migration.Name, err)
		}
		if _, err := db.Exec(
			"INSERT INTO schema_migrations(version, name, applied_at) VALUES(?, ?, ?)",
			migration.Version,
			migration.Name,
			time.Now().UTC().Format(time.RFC3339),
		); err != nil {
			return fmt.Errorf("record migration %03d_%s: %w", migration.Version, migration.Name, err)
		}
	}
	return nil
}

func migrationApplied(db *sql.DB, version int) (bool, error) {
	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM schema_migrations WHERE version = ?", version).Scan(&count); err != nil {
		return false, err
	}
	return count > 0, nil
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
