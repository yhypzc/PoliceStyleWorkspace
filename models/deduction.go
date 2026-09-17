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
	IncludeWeekly        bool     `json:"include_weekly"`
	SchoolSupervision    bool     `json:"-"`
}

// CreateDeductionTable creates the regular-deduction table. include_weekly is a
// numeric flag on the single-deduction record: 0 means the record is excluded
// from the squadron weekly deduction, any other value means it is included.
func CreateDeductionTable(db *sql.DB) error {
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS police_style_records_single_subrecords (
		id CHAR(32) PRIMARY KEY,
		submit_date TEXT,
		student_name VARCHAR(255),
		content TEXT,
		score REAL DEFAULT 0.0,
		include_weekly INTEGER NOT NULL DEFAULT 1
	)`); err != nil {
		return err
	}
	return ensureIncludeWeeklyColumn(db)
}

// ensureIncludeWeeklyColumn upgrades databases created before include_weekly
// existed. Existing rows keep counting toward the weekly deduction.
func ensureIncludeWeeklyColumn(db *sql.DB) error {
	exists := false
	rows, err := db.Query(`PRAGMA table_info(police_style_records_single_subrecords)`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var cid, notNull, pk int
		var name, columnType string
		var defaultValue any
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &pk); err != nil {
			rows.Close()
			return err
		}
		if name == "include_weekly" {
			exists = true
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	if exists {
		return nil
	}
	_, err = db.Exec(`ALTER TABLE police_style_records_single_subrecords ADD COLUMN include_weekly INTEGER NOT NULL DEFAULT 1`)
	return err
}

// includeWeeklyValue converts the boolean flag into the stored numeric value
// (0 = not counted, 1 = counted).
func includeWeeklyValue(include bool) int {
	if include {
		return 1
	}
	return 0
}

func ListDeductionRecords(db *sql.DB) ([]DeductionRecord, error) {
	rows, err := db.Query(`SELECT r.id, r.submit_date, r.student_name,
		COALESCE(GROUP_CONCAT(s.stu_name, ', '), ''),
		COALESCE(GROUP_CONCAT(o.student_id, ','), ''), r.content, r.score, r.include_weekly
		FROM police_style_records_single_subrecords r
		LEFT JOIN ownership_single_subrecords o ON o.record_id = r.id
		LEFT JOIN students s ON s.id = o.student_id
		GROUP BY r.id, r.submit_date, r.student_name, r.content, r.score, r.include_weekly
		ORDER BY r.id ASC`)
	if err != nil {
		return nil, fmt.Errorf("查询扣分记录失败: %w", err)
	}
	defer rows.Close()
	var list = make([]DeductionRecord, 0)
	for rows.Next() {
		var r DeductionRecord
		var studentIDs string
		var includeWeekly int
		if err := rows.Scan(&r.ID, &r.SubmitDate, &r.StudentName, &r.RecognizedStudents, &studentIDs, &r.Content, &r.Score, &includeWeekly); err != nil {
			return nil, fmt.Errorf("扫描扣分记录失败: %w", err)
		}
		r.IncludeWeekly = includeWeekly != 0
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
	// 新建/导入的记录默认计入区队周扣分（与 INSERT 中写死的 1 保持一致）
	r.IncludeWeekly = true
	_, err := db.Exec(
		`INSERT INTO police_style_records_single_subrecords (id, submit_date, student_name, content, score, include_weekly) VALUES (?, ?, ?, ?, ?, 1)`,
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

// CreateUnassignedDeductionRecord inserts a single deduction record without
// any student ownership, representing the local "未指定/未认定" item that the
// workspace lists for later recognition/assignment. studentName may be empty.
func CreateUnassignedDeductionRecord(db *sql.DB, r DeductionRecord) (*DeductionRecord, error) {
	r.StudentID = ""
	r.StudentName = strings.TrimSpace(r.StudentName)
	r.ID = generateDeductionRecordID(r)
	// 新建/导入的记录默认计入区队周扣分（与 INSERT 中写死的 1 保持一致）
	r.IncludeWeekly = true
	_, err := db.Exec(
		`INSERT INTO police_style_records_single_subrecords (id, submit_date, student_name, content, score, include_weekly) VALUES (?, ?, ?, ?, ?, 1)`,
		r.ID, r.SubmitDate, r.StudentName, r.Content, r.Score,
	)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint") {
			return nil, fmt.Errorf("记录ID %q 已存在", r.ID)
		}
		return nil, fmt.Errorf("创建未指定扣分记录失败: %w", err)
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
	// 新建/导入的记录默认计入区队周扣分（与 INSERT 中写死的 1 保持一致）
	r.IncludeWeekly = true
	if _, err := tx.Exec(
		`INSERT INTO police_style_records_single_subrecords (id, submit_date, student_name, content, score, include_weekly) VALUES (?, ?, ?, ?, ?, 1)`,
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

// CreateDeductionRecordWithOwnership inserts a manually added regular-deduction
// record (the 「添加项目」 dialog). It honours both switches the dialog exposes —
// whether the record counts toward the squadron weekly deduction and whether it
// is a 校督 (school-supervision) record, which drives the `xd_` ID prefix and
// therefore the appeal template — and attaches the selected 认定 students
// (ownership rows) in the same transaction. 姓名 may be typed by hand or left
// empty, in which case it is derived from the recognized students.
func CreateDeductionRecordWithOwnership(db *sql.DB, r DeductionRecord, studentIDs []string) (*DeductionRecord, error) {
	studentIDs = splitStudentIDs(strings.Join(studentIDs, ","))
	r.StudentName = strings.TrimSpace(r.StudentName)
	r.SubmitDate = strings.TrimSpace(r.SubmitDate)
	r.Content = strings.TrimSpace(r.Content)
	if r.SubmitDate == "" {
		return nil, errors.New("日期不能为空")
	}
	if r.StudentName == "" && len(studentIDs) == 0 {
		return nil, errors.New("请填写姓名或选择认定学生")
	}

	tx, err := db.Begin()
	if err != nil {
		return nil, fmt.Errorf("创建扣分记录失败: %w", err)
	}
	defer tx.Rollback()

	names := make([]string, 0, len(studentIDs))
	for _, studentID := range studentIDs {
		var studentName string
		if err := tx.QueryRow(`SELECT stu_name FROM students WHERE id = ?`, studentID).Scan(&studentName); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil, fmt.Errorf("学号 %q 不存在", studentID)
			}
			return nil, fmt.Errorf("查询学生失败: %w", err)
		}
		names = append(names, studentName)
	}
	r.StudentID = strings.Join(studentIDs, ",")
	if r.StudentName == "" {
		r.StudentName = strings.Join(names, ", ")
	}
	r.RecognizedStudentIDs = studentIDs
	r.RecognizedStudents = strings.Join(names, ", ")
	r.ID = generateDeductionRecordID(r)
	if _, err := tx.Exec(
		`INSERT INTO police_style_records_single_subrecords (id, submit_date, student_name, content, score, include_weekly) VALUES (?, ?, ?, ?, ?, ?)`,
		r.ID, r.SubmitDate, r.StudentName, r.Content, r.Score, includeWeeklyValue(r.IncludeWeekly),
	); err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint") {
			return nil, fmt.Errorf("记录ID %q 已存在", r.ID)
		}
		return nil, fmt.Errorf("创建扣分记录失败: %w", err)
	}
	for _, studentID := range studentIDs {
		if _, err := tx.Exec(`INSERT INTO ownership_single_subrecords (record_id, student_id) VALUES (?, ?)`, r.ID, studentID); err != nil {
			return nil, fmt.Errorf("保存认定失败: %w", err)
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
		`UPDATE police_style_records_single_subrecords SET submit_date=?, student_name=?, content=?, score=?, include_weekly=? WHERE id=?`,
		r.SubmitDate, r.StudentName, r.Content, r.Score, includeWeeklyValue(r.IncludeWeekly), id,
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
