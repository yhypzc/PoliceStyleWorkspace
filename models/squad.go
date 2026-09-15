package models

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

// defaultSquadName seeds the squadron table on first run. The daily
// 警务化管理通报 workbooks list every 区队, and the importer only keeps the
// rows belonging to the configured squadron.
const defaultSquadName = "24网安二"

// CreateSquadTable creates the single-column squadron table and seeds the
// default name when the table is empty.
func CreateSquadTable(db *sql.DB) error {
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS squad (
		squad_name TEXT PRIMARY KEY
	)`); err != nil {
		return err
	}
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM squad`).Scan(&count); err != nil {
		return err
	}
	if count > 0 {
		return nil
	}
	_, err := db.Exec(`INSERT INTO squad (squad_name) VALUES (?)`, defaultSquadName)
	return err
}

// GetSquadName returns the configured squadron name. An empty table yields "".
func GetSquadName(db *sql.DB) (string, error) {
	var name string
	err := db.QueryRow(`SELECT squad_name FROM squad LIMIT 1`).Scan(&name)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("查询区队名称失败: %w", err)
	}
	return name, nil
}

// SetSquadName replaces the stored squadron name; the table always holds at
// most one row.
func SetSquadName(db *sql.DB, name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return errors.New("区队名称不能为空")
	}
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("保存区队名称失败: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM squad`); err != nil {
		return fmt.Errorf("保存区队名称失败: %w", err)
	}
	if _, err := tx.Exec(`INSERT INTO squad (squad_name) VALUES (?)`, name); err != nil {
		return fmt.Errorf("保存区队名称失败: %w", err)
	}
	return tx.Commit()
}
