package models

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

type Student struct {
	ID   string `json:"id"`
	Name string `json:"stu_name"`
}

func CreateStudentsTable(db *sql.DB) error {
	_, err := db.Exec(`CREATE TABLE IF NOT EXISTS students (
		id CHAR(6) PRIMARY KEY,
		stu_name VARCHAR(10) NOT NULL
	)`)
	return err
}

func ListStudents(db *sql.DB) ([]Student, error) {
	rows, err := db.Query(`SELECT id, stu_name FROM students ORDER BY id ASC`)
	if err != nil {
		return nil, fmt.Errorf("查询学生列表失败: %w", err)
	}
	defer rows.Close()
	var list = make([]Student, 0)
	for rows.Next() {
		var s Student
		if err := rows.Scan(&s.ID, &s.Name); err != nil {
			return nil, fmt.Errorf("扫描学生数据失败: %w", err)
		}
		list = append(list, s)
	}
	return list, rows.Err()
}

func CreateStudent(db *sql.DB, id, name string) (*Student, error) {
	id = strings.TrimSpace(id)
	name = strings.TrimSpace(name)
	if id == "" || name == "" {
		return nil, errors.New("学号和姓名不能为空")
	}
	_, err := db.Exec(`INSERT INTO students (id, stu_name) VALUES (?, ?)`, id, name)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint") {
			return nil, fmt.Errorf("学号 %q 已存在", id)
		}
		return nil, fmt.Errorf("创建学生失败: %w", err)
	}
	return &Student{ID: id, Name: name}, nil
}

func UpdateStudent(db *sql.DB, id, name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return errors.New("姓名不能为空")
	}
	res, err := db.Exec(`UPDATE students SET stu_name = ? WHERE id = ?`, name, id)
	if err != nil {
		return fmt.Errorf("更新学生失败: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return errors.New("学生不存在")
	}
	return nil
}

func DeleteStudent(db *sql.DB, id string) error {
	id = strings.TrimSpace(id)
	res, err := db.Exec(`DELETE FROM students WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("删除学生失败: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return errors.New("学生不存在")
	}
	return nil
}
