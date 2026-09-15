package models

import (
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

type Student struct {
	ID          string `json:"id"`
	Name        string `json:"stu_name"`
	PhoneNumber string `json:"phone_number"`
}

func CreateStudentsTable(db *sql.DB) error {
	_, err := db.Exec(`CREATE TABLE IF NOT EXISTS students (
		id CHAR(6) PRIMARY KEY,
		stu_name VARCHAR(10) NOT NULL,
		phone_number TEXT
	)`)
	if err != nil {
		return err
	}
	return ensureStudentsPhoneColumn(db)
}

// ensureStudentsPhoneColumn adds phone_number to student tables created before
// the broadcast-event @ mention feature introduced student mobile numbers.
func ensureStudentsPhoneColumn(db *sql.DB) error {
	rows, err := db.Query(`PRAGMA table_info(students)`)
	if err != nil {
		return err
	}
	has := false
	for rows.Next() {
		var cid, notnull, pk int
		var name, typ string
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &typ, &notnull, &dflt, &pk); err != nil {
			rows.Close()
			return err
		}
		if name == "phone_number" {
			has = true
		}
	}
	rows.Close()
	if has {
		return nil
	}
	_, err = db.Exec(`ALTER TABLE students ADD COLUMN phone_number TEXT`)
	return err
}

// NormalizePhoneNumber cleans up values coming from Excel or the UI: numeric
// cells may arrive as "15397085895", "15397085895.0" or "1.53971E+10", and
// users often paste numbers with spaces or dashes.
func NormalizePhoneNumber(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	replacer := strings.NewReplacer(" ", "", "\u3000", "", "-", "", "－", "", "(", "", ")", "", "（", "", "）", "")
	value = replacer.Replace(value)
	if strings.HasSuffix(value, ".0") && isAllDigits(strings.TrimSuffix(value, ".0")) {
		return strings.TrimSuffix(value, ".0")
	}
	if expanded, ok := expandScientificNotation(value); ok {
		return expanded
	}
	return value
}

func isAllDigits(value string) bool {
	if value == "" {
		return false
	}
	for _, r := range value {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// expandScientificNotation turns "1.53971E+10" into "15397100000".
func expandScientificNotation(value string) (string, bool) {
	upper := strings.ToUpper(value)
	index := strings.IndexAny(upper, "E")
	if index <= 0 {
		return "", false
	}
	mantissa, exponentText := upper[:index], upper[index+1:]
	exponent, err := strconv.Atoi(exponentText)
	if err != nil || exponent <= 0 || exponent > 20 {
		return "", false
	}
	digits := strings.Replace(mantissa, ".", "", 1)
	dot := strings.Index(mantissa, ".")
	scale := 0
	if dot >= 0 {
		scale = len(mantissa) - dot - 1
	}
	if !isAllDigits(digits) {
		return "", false
	}
	if scale > exponent {
		return "", false
	}
	return digits + strings.Repeat("0", exponent-scale), true
}

func ListStudents(db *sql.DB) ([]Student, error) {
	rows, err := db.Query(`SELECT id, stu_name, COALESCE(phone_number, '') FROM students ORDER BY id ASC`)
	if err != nil {
		return nil, fmt.Errorf("查询学生列表失败: %w", err)
	}
	defer rows.Close()
	var list = make([]Student, 0)
	for rows.Next() {
		var s Student
		if err := rows.Scan(&s.ID, &s.Name, &s.PhoneNumber); err != nil {
			return nil, fmt.Errorf("扫描学生数据失败: %w", err)
		}
		list = append(list, s)
	}
	return list, rows.Err()
}

func CreateStudent(db *sql.DB, id, name, phoneNumber string) (*Student, error) {
	id = strings.TrimSpace(id)
	name = strings.TrimSpace(name)
	phoneNumber = NormalizePhoneNumber(phoneNumber)
	if id == "" || name == "" {
		return nil, errors.New("学号和姓名不能为空")
	}
	_, err := db.Exec(`INSERT INTO students (id, stu_name, phone_number) VALUES (?, ?, ?)`, id, name, nullableString(phoneNumber))
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint") {
			return nil, fmt.Errorf("学号 %q 已存在", id)
		}
		return nil, fmt.Errorf("创建学生失败: %w", err)
	}
	return &Student{ID: id, Name: name, PhoneNumber: phoneNumber}, nil
}

func UpdateStudent(db *sql.DB, id, name, phoneNumber string) error {
	name = strings.TrimSpace(name)
	phoneNumber = NormalizePhoneNumber(phoneNumber)
	if name == "" {
		return errors.New("姓名不能为空")
	}
	res, err := db.Exec(`UPDATE students SET stu_name = ?, phone_number = ? WHERE id = ?`, name, nullableString(phoneNumber), id)
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

// StudentPhoneByName resolves a student name to the configured mobile number.
// Names are not unique in the students table, so the first non-empty match in
// id order wins.
func StudentPhoneByName(db *sql.DB, name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", nil
	}
	var phone string
	err := db.QueryRow(
		`SELECT COALESCE(phone_number, '') FROM students WHERE stu_name = ? AND COALESCE(phone_number, '') <> '' ORDER BY id ASC LIMIT 1`,
		name,
	).Scan(&phone)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return phone, err
}

// StudentsWithPhone returns every student that has a mobile number configured,
// which is the candidate set for broadcast-event @ mentions.
func StudentsWithPhone(db *sql.DB) ([]Student, error) {
	rows, err := db.Query(`SELECT id, stu_name, phone_number FROM students WHERE COALESCE(phone_number, '') <> '' ORDER BY id ASC`)
	if err != nil {
		return nil, fmt.Errorf("查询学生手机号失败: %w", err)
	}
	defer rows.Close()
	list := make([]Student, 0)
	for rows.Next() {
		var s Student
		if err := rows.Scan(&s.ID, &s.Name, &s.PhoneNumber); err != nil {
			return nil, fmt.Errorf("扫描学生手机号失败: %w", err)
		}
		list = append(list, s)
	}
	return list, rows.Err()
}
