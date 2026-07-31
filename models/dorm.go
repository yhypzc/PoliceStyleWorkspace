package models

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

type Dorm struct {
	Name        string `json:"dorm_name"`
	Seq         int    `json:"seq"`
	PhoneNumber string `json:"phone_number"`
}

func CreateDormTable(db *sql.DB) error {
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS dorm (dorm_name TEXT PRIMARY KEY, seq INT, phone_number TEXT)`); err != nil {
		return err
	}
	return ensureDormPhoneNumberColumn(db)
}

func ListDorms(db *sql.DB) ([]Dorm, error) {
	rows, err := db.Query(`SELECT dorm_name, seq, COALESCE(phone_number, '') FROM dorm ORDER BY seq ASC, dorm_name ASC`)
	if err != nil {
		return nil, fmt.Errorf("查询寝室列表失败: %w", err)
	}
	defer rows.Close()
	dorms := make([]Dorm, 0)
	for rows.Next() {
		var dorm Dorm
		if err := rows.Scan(&dorm.Name, &dorm.Seq, &dorm.PhoneNumber); err != nil {
			return nil, fmt.Errorf("读取寝室数据失败: %w", err)
		}
		dorms = append(dorms, dorm)
	}
	return dorms, rows.Err()
}

func CreateDorm(db *sql.DB, name, phoneNumber string) (*Dorm, error) {
	name = strings.TrimSpace(name)
	phoneNumber = strings.TrimSpace(phoneNumber)
	if name == "" {
		return nil, errors.New("寝室名称不能为空")
	}
	var maxSeq int
	if err := db.QueryRow(`SELECT COALESCE(MAX(seq), 0) FROM dorm`).Scan(&maxSeq); err != nil {
		return nil, fmt.Errorf("确定寝室顺序失败: %w", err)
	}
	dorm := &Dorm{Name: name, Seq: maxSeq + 1, PhoneNumber: phoneNumber}
	if _, err := db.Exec(`INSERT INTO dorm (dorm_name, seq, phone_number) VALUES (?, ?, ?)`, dorm.Name, dorm.Seq, dorm.PhoneNumber); err != nil {
		return nil, fmt.Errorf("新增寝室失败: %w", err)
	}
	return dorm, nil
}

func UpdateDorm(db *sql.DB, oldName, newName, phoneNumber string) error {
	newName = strings.TrimSpace(newName)
	phoneNumber = strings.TrimSpace(phoneNumber)
	if newName == "" {
		return errors.New("寝室名称不能为空")
	}
	result, err := db.Exec(`UPDATE dorm SET dorm_name = ?, phone_number = ? WHERE dorm_name = ?`, newName, phoneNumber, oldName)
	if err != nil {
		return fmt.Errorf("更新寝室失败: %w", err)
	}
	if n, _ := result.RowsAffected(); n == 0 {
		return errors.New("寝室不存在")
	}
	return nil
}

func DeleteDorm(db *sql.DB, name string) error {
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("创建删除事务失败: %w", err)
	}
	defer tx.Rollback()
	result, err := tx.Exec(`DELETE FROM dorm WHERE dorm_name = ?`, name)
	if err != nil {
		return fmt.Errorf("删除寝室失败: %w", err)
	}
	if n, _ := result.RowsAffected(); n == 0 {
		return errors.New("寝室不存在")
	}
	rows, err := tx.Query(`SELECT dorm_name FROM dorm ORDER BY seq ASC, dorm_name ASC`)
	if err != nil {
		return fmt.Errorf("查询剩余寝室失败: %w", err)
	}
	names := make([]string, 0)
	for rows.Next() {
		var dormName string
		if err := rows.Scan(&dormName); err != nil {
			return fmt.Errorf("读取剩余寝室失败: %w", err)
		}
		names = append(names, dormName)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return fmt.Errorf("遍历剩余寝室失败: %w", err)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("关闭寝室查询失败: %w", err)
	}
	for index, dormName := range names {
		if _, err := tx.Exec(`UPDATE dorm SET seq = ? WHERE dorm_name = ?`, index+1, dormName); err != nil {
			return fmt.Errorf("重排寝室编号失败: %w", err)
		}
	}
	return tx.Commit()
}

func ReorderDorms(db *sql.DB, names []string) error {
	dorms, err := ListDorms(db)
	if err != nil {
		return err
	}
	if len(names) != len(dorms) {
		return errors.New("寝室排序数据不完整，请刷新后重试")
	}
	existing := make(map[string]bool, len(dorms))
	for _, dorm := range dorms {
		existing[dorm.Name] = true
	}
	seen := make(map[string]bool, len(names))
	for _, name := range names {
		if !existing[name] || seen[name] {
			return errors.New("寝室排序数据无效，请刷新后重试")
		}
		seen[name] = true
	}
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("创建排序事务失败: %w", err)
	}
	defer tx.Rollback()
	for index, name := range names {
		if _, err := tx.Exec(`UPDATE dorm SET seq = ? WHERE dorm_name = ?`, index+1, name); err != nil {
			return fmt.Errorf("保存寝室排序失败: %w", err)
		}
	}
	return tx.Commit()
}

func ensureDormPhoneNumberColumn(db *sql.DB) error {
	rows, err := db.Query(`PRAGMA table_info(dorm)`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var name, columnType string
		var notNull, pk int
		var defaultValue any
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &pk); err != nil {
			return err
		}
		if name == "phone_number" {
			return rows.Err()
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	_, err = db.Exec(`ALTER TABLE dorm ADD COLUMN phone_number TEXT`)
	return err
}
