package models

import (
	"crypto/md5"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

type DeductionRecord struct {
	ID                   string   `json:"id"`
	SubmitDate           string   `json:"submit_date"`
	StudentName          string   `json:"student_name"`
	RecognizedStudents   string   `json:"recognized_students"`
	RecognizedStudentIDs []string `json:"recognized_student_ids"`
	StudentID            string   `json:"student_id,omitempty"`
	Content              string   `json:"content"`
	Score                float64  `json:"score"`
	SchoolSupervision    bool     `json:"-"`
}

func CreateDeductionTable(db *sql.DB) error {
	_, err := db.Exec(`CREATE TABLE IF NOT EXISTS police_style_records_single_subrecords (
		id CHAR(32) PRIMARY KEY,
		submit_date TEXT,
		student_name VARCHAR(255),
		content TEXT,
		score REAL DEFAULT 0.0
	)`)
	return err
}

func ListDeductionRecords(db *sql.DB) ([]DeductionRecord, error) {
	rows, err := db.Query(`SELECT r.id, r.submit_date, r.student_name,
		COALESCE(GROUP_CONCAT(s.stu_name, ', '), ''),
		COALESCE(GROUP_CONCAT(o.student_id, ','), ''), r.content, r.score
		FROM police_style_records_single_subrecords r
		LEFT JOIN ownership_single_subrecords o ON o.record_id = r.id
		LEFT JOIN students s ON s.id = o.student_id
		GROUP BY r.id, r.submit_date, r.student_name, r.content, r.score
		ORDER BY r.id ASC`)
	if err != nil {
		return nil, fmt.Errorf("查询扣分记录失败: %w", err)
	}
	defer rows.Close()
	var list = make([]DeductionRecord, 0)
	for rows.Next() {
		var r DeductionRecord
		var studentIDs string
		if err := rows.Scan(&r.ID, &r.SubmitDate, &r.StudentName, &r.RecognizedStudents, &studentIDs, &r.Content, &r.Score); err != nil {
			return nil, fmt.Errorf("扫描扣分记录失败: %w", err)
		}
		r.RecognizedStudentIDs = splitStudentIDs(studentIDs)
		list = append(list, r)
	}
	return list, rows.Err()
}

func ReplaceDeductionRecordStudents(db *sql.DB, recordID string, studentIDs []string) error {
	studentIDs = splitStudentIDs(strings.Join(studentIDs, ","))
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("更新认定失败: %w", err)
	}
	defer tx.Rollback()

	var exists int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM police_style_records_single_subrecords WHERE id = ?`, recordID).Scan(&exists); err != nil {
		return fmt.Errorf("查询扣分记录失败: %w", err)
	}
	if exists == 0 {
		return errors.New("扣分记录不存在")
	}
	for _, studentID := range studentIDs {
		var studentExists int
		if err := tx.QueryRow(`SELECT COUNT(*) FROM students WHERE id = ?`, studentID).Scan(&studentExists); err != nil {
			return fmt.Errorf("查询学生失败: %w", err)
		}
		if studentExists == 0 {
			return fmt.Errorf("学号 %q 不存在", studentID)
		}
	}
	if _, err := tx.Exec(`DELETE FROM ownership_single_subrecords WHERE record_id = ?`, recordID); err != nil {
		return fmt.Errorf("删除原认定失败: %w", err)
	}
	for _, studentID := range studentIDs {
		if _, err := tx.Exec(`INSERT INTO ownership_single_subrecords (record_id, student_id) VALUES (?, ?)`, recordID, studentID); err != nil {
			return fmt.Errorf("保存认定失败: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("保存认定失败: %w", err)
	}
	return nil
}

func CreateDeductionRecord(db *sql.DB, r DeductionRecord) (*DeductionRecord, error) {
	r.StudentName = strings.TrimSpace(r.StudentName)
	if r.StudentName == "" {
		return nil, errors.New("学生姓名不能为空")
	}
	r.ID = generateDeductionRecordID(r)
	_, err := db.Exec(
		`INSERT INTO police_style_records_single_subrecords (id, submit_date, student_name, content, score) VALUES (?, ?, ?, ?, ?)`,
		r.ID, r.SubmitDate, r.StudentName, r.Content, r.Score,
	)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint") {
			return nil, fmt.Errorf("记录ID %q 已存在", r.ID)
		}
		return nil, fmt.Errorf("创建扣分记录失败: %w", err)
	}
	return &r, nil
}

// CreateDeductionRecordForStudents records a violation against one or more existing students.
// The record and ownership mapping are committed atomically.
func CreateDeductionRecordForStudents(db *sql.DB, r DeductionRecord, studentIDs string) (*DeductionRecord, error) {
	studentIDList := splitStudentIDs(studentIDs)
	if len(studentIDList) == 0 {
		return nil, errors.New("违规学号不能为空")
	}

	tx, err := db.Begin()
	if err != nil {
		return nil, fmt.Errorf("创建扣分记录失败: %w", err)
	}
	defer tx.Rollback()

	studentNames := make([]string, 0, len(studentIDList))
	for _, studentID := range studentIDList {
		var studentName string
		if err := tx.QueryRow(`SELECT stu_name FROM students WHERE id = ?`, studentID).Scan(&studentName); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil, fmt.Errorf("学号 %q 不存在", studentID)
			}
			return nil, fmt.Errorf("查询学生失败: %w", err)
		}
		studentNames = append(studentNames, studentName)
	}
	r.StudentID = strings.Join(studentIDList, ",")
	if strings.TrimSpace(r.StudentName) == "" {
		r.StudentName = strings.Join(studentNames, ", ")
	}
	r.ID = generateDeductionRecordID(r)
	if _, err := tx.Exec(
		`INSERT INTO police_style_records_single_subrecords (id, submit_date, student_name, content, score) VALUES (?, ?, ?, ?, ?)`,
		r.ID, r.SubmitDate, r.StudentName, r.Content, r.Score,
	); err != nil {
		return nil, fmt.Errorf("创建扣分记录失败: %w", err)
	}
	for _, studentID := range studentIDList {
		if _, err := tx.Exec(`INSERT INTO ownership_single_subrecords (record_id, student_id) VALUES (?, ?)`, r.ID, studentID); err != nil {
			return nil, fmt.Errorf("创建违规学生关联失败: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("保存扣分记录失败: %w", err)
	}
	return &r, nil
}

func splitStudentIDs(value string) []string {
	seen := make(map[string]struct{})
	studentIDs := make([]string, 0)
	for _, studentID := range strings.Split(value, ",") {
		studentID = strings.TrimSpace(studentID)
		if studentID == "" {
			continue
		}
		if _, exists := seen[studentID]; exists {
			continue
		}
		seen[studentID] = struct{}{}
		studentIDs = append(studentIDs, studentID)
	}
	return studentIDs
}

func generateDeductionRecordID(r DeductionRecord) string {
	value := strings.Join([]string{r.SubmitDate, r.StudentID, r.StudentName, r.Content, fmt.Sprintf("%g", r.Score)}, "\x1f")
	id := fmt.Sprintf("%x", md5.Sum([]byte(value)))
	if r.SchoolSupervision {
		return "xd_" + id[3:]
	}
	return id
}

func UpdateDeductionRecord(db *sql.DB, id string, r DeductionRecord) error {
	r.StudentName = strings.TrimSpace(r.StudentName)
	if r.StudentName == "" {
		return errors.New("学生姓名不能为空")
	}
	res, err := db.Exec(
		`UPDATE police_style_records_single_subrecords SET submit_date=?, student_name=?, content=?, score=? WHERE id=?`,
		r.SubmitDate, r.StudentName, r.Content, r.Score, id,
	)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint") {
			return fmt.Errorf("记录ID %q 已存在", r.ID)
		}
		return fmt.Errorf("更新扣分记录失败: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return errors.New("扣分记录不存在")
	}
	return nil
}

func DeleteDeductionRecord(db *sql.DB, id string) error {
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("删除扣分记录失败: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM ownership_single_subrecords WHERE record_id = ?`, id); err != nil {
		return fmt.Errorf("删除认定关联失败: %w", err)
	}
	res, err := tx.Exec(`DELETE FROM police_style_records_single_subrecords WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("删除扣分记录失败: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return errors.New("扣分记录不存在")
	}
	return tx.Commit()
}

func DeleteDeductionRecords(db *sql.DB, ids []string) (int, error) {
	uniqueIDs := make(map[string]struct{})
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id != "" {
			uniqueIDs[id] = struct{}{}
		}
	}
	if len(uniqueIDs) == 0 {
		return 0, errors.New("请选择要删除的扣分记录")
	}
	tx, err := db.Begin()
	if err != nil {
		return 0, fmt.Errorf("批量删除扣分记录失败: %w", err)
	}
	defer tx.Rollback()
	deleted := 0
	for id := range uniqueIDs {
		if _, err := tx.Exec(`DELETE FROM ownership_single_subrecords WHERE record_id = ?`, id); err != nil {
			return 0, fmt.Errorf("删除认定关联失败: %w", err)
		}
		result, err := tx.Exec(`DELETE FROM police_style_records_single_subrecords WHERE id = ?`, id)
		if err != nil {
			return 0, fmt.Errorf("删除扣分记录失败: %w", err)
		}
		affected, _ := result.RowsAffected()
		deleted += int(affected)
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("保存删除结果失败: %w", err)
	}
	return deleted, nil
}

func CreateOwnershipSingleTable(db *sql.DB) error {
	_, err := db.Exec(`CREATE TABLE IF NOT EXISTS ownership_single_subrecords (
		record_id CHAR(32),
		student_id CHAR(6),
		PRIMARY KEY (record_id, student_id),
		FOREIGN KEY (student_id) REFERENCES students(id),
		FOREIGN KEY (record_id) REFERENCES police_style_records_single_subrecords(id)
	)`)
	return err
}
