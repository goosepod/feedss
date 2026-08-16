package main

import (
	"database/sql"
	"path/filepath"
	"testing"
)

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
