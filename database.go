package main

import (
        "database/sql"

        _ "github.com/mattn/go-sqlite3"
)

type Database struct {
        db *sql.DB
}

func NewDatabase(dbPath string) (*Database, error) {
        db, err := sql.Open("sqlite3", dbPath)
        if err != nil {
                return nil, err
        }
        d := &Database{db: db}
        if err := d.init(); err != nil {
                return nil, err
        }
        return d, nil
}

func (d *Database) init() error {
        query := `
        CREATE TABLE IF NOT EXISTS sent_images (
                id TEXT PRIMARY KEY
        );`
        _, err := d.db.Exec(query)
        return err
}

func (d *Database) IsSent(imageID string) (bool, error) {
        var id string
        err := d.db.QueryRow("SELECT id FROM sent_images WHERE id = ?", imageID).Scan(&id)
        if err == sql.ErrNoRows {
                return false, nil
        }
        if err != nil {
                return false, err
        }
        return true, nil
}

func (d *Database) MarkSent(imageID string) error {
        _, err := d.db.Exec("INSERT OR IGNORE INTO sent_images(id) VALUES (?)", imageID)
        return err
}
