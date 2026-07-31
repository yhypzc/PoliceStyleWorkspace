package models

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"math/big"

	_ "modernc.org/sqlite"
)

const adminUser = "admin"

type User struct {
	Username string `json:"username"`
}

func Init(dbPath string) (*sql.DB, string, error) {
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, "", err
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	if err := configureSQLite(db); err != nil {
		_ = db.Close()
		return nil, "", err
	}
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS user (
		username TEXT PRIMARY KEY,
		password CHAR(32) NOT NULL,
		salt CHAR(16) NOT NULL
	)`); err != nil {
		return nil, "", err
	}
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS semester (
		semester_name VARCHAR(255) PRIMARY KEY,
		start_time TEXT,
		end_time TEXT
	)`); err != nil {
		return nil, "", err
	}
	if err := CreateStudentsTable(db); err != nil {
		return nil, "", err
	}
	if err := CreateDormTable(db); err != nil {
		return nil, "", err
	}
	if err := CreateDeductionTable(db); err != nil {
		return nil, "", err
	}
	if err := CreateOwnershipSingleTable(db); err != nil {
		return nil, "", err
	}
	if err := CreateMultiDeductionTables(db); err != nil {
		return nil, "", err
	}
	if err := CreateDailyReportTables(db); err != nil {
		return nil, "", err
	}

	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM user WHERE username = ?`, adminUser).Scan(&count); err != nil {
		return nil, "", err
	}
	if count > 0 {
		return db, "", nil
	}

	plain, err := RandomString(12)
	if err != nil {
		return nil, "", err
	}
	salt, err := RandomString(16)
	if err != nil {
		return nil, "", err
	}
	if _, err := db.Exec(`INSERT INTO user(username, password, salt) VALUES (?, ?, ?)`, adminUser, HashPassword(plain, salt), salt); err != nil {
		return nil, "", err
	}
	return db, plain, nil
}

func configureSQLite(db *sql.DB) error {
	_, err := db.Exec(`
		PRAGMA foreign_keys = ON;
		PRAGMA busy_timeout = 5000;
		PRAGMA journal_mode = WAL;
		PRAGMA synchronous = NORMAL;
	`)
	return err
}

func ValidateLogin(db *sql.DB, username, password string) (bool, error) {
	if username != adminUser {
		return false, nil
	}
	var stored, salt string
	err := db.QueryRow(`SELECT password, salt FROM user WHERE username = ?`, username).Scan(&stored, &salt)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return stored == HashPassword(password, salt), nil
}

func ChangePassword(db *sql.DB, oldPassword, newPassword string) error {
	ok, err := ValidateLogin(db, adminUser, oldPassword)
	if err != nil {
		return err
	}
	if !ok {
		return errors.New("旧密码不正确")
	}
	salt, err := RandomString(16)
	if err != nil {
		return err
	}
	_, err = db.Exec(`UPDATE user SET password = ?, salt = ? WHERE username = ?`, HashPassword(newPassword, salt), salt, adminUser)
	return err
}

func HashPassword(plain, salt string) string {
	first := sha256.Sum256([]byte(plain))
	firstHex := hex.EncodeToString(first[:])
	second := sha256.Sum256([]byte(firstHex + salt))
	return hex.EncodeToString(second[:])
}

func RandomString(length int) (string, error) {
	const alphabet = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	out := make([]byte, length)
	for i := range out {
		n, err := rand.Int(rand.Reader, big.NewInt(int64(len(alphabet))))
		if err != nil {
			return "", err
		}
		out[i] = alphabet[n.Int64()]
	}
	return string(out), nil
}
