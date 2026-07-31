package models

import (
	"crypto/md5"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

type MultiDeductionRecord struct {
	ID         string  `json:"id"`
	SubmitDate string  `json:"submit_date"`
	DormName   string  `json:"dorm_name"`
	Content    string  `json:"content"`
	Score      float64 `json:"score"`
}

type MultiDeductionSubrecord struct {
	ID           string   `json:"id"`
	BelongsTo    string   `json:"belongs_to"`
	Content      string   `json:"content"`
	StudentIDs   []string `json:"student_ids"`
	StudentNames string   `json:"student_names"`
}

func CreateMultiDeductionTables(db *sql.DB) error {
	for _, statement := range []string{
		`CREATE TABLE IF NOT EXISTS police_style_records_multi_subrecords (id CHAR(32) PRIMARY KEY, submit_date TEXT, dorm_name VARCHAR(255), content TEXT, score REAL DEFAULT 0.0)`,
		`CREATE TABLE IF NOT EXISTS subrecords_for_police_style_records_multi_subrecords (id CHAR(32) PRIMARY KEY, belongs_to CHAR(32), content TEXT, FOREIGN KEY (belongs_to) REFERENCES police_style_records_multi_subrecords(id))`,
		`CREATE TABLE IF NOT EXISTS ownership_multi_subrecords (subrecord_id CHAR(32), student_id CHAR(6), PRIMARY KEY (subrecord_id, student_id), FOREIGN KEY (student_id) REFERENCES students(id), FOREIGN KEY (subrecord_id) REFERENCES subrecords_for_police_style_records_multi_subrecords(id))`,
	} {
		if _, err := db.Exec(statement); err != nil {
			return err
		}
	}
	return nil
}

func ListMultiDeductionRecords(db *sql.DB) ([]MultiDeductionRecord, error) {
	rows, err := db.Query(`SELECT id, submit_date, dorm_name, content, score FROM police_style_records_multi_subrecords ORDER BY submit_date DESC, id DESC`)
	if err != nil {
		return nil, fmt.Errorf("查询寝室整体差记录失败: %w", err)
	}
	defer rows.Close()
	records := make([]MultiDeductionRecord, 0)
	for rows.Next() {
		var r MultiDeductionRecord
		if err := rows.Scan(&r.ID, &r.SubmitDate, &r.DormName, &r.Content, &r.Score); err != nil {
			return nil, err
		}
		records = append(records, r)
	}
	return records, rows.Err()
}

func CreateMultiDeductionRecord(db *sql.DB, r MultiDeductionRecord) (*MultiDeductionRecord, error) {
	r.DormName, r.Content = strings.TrimSpace(r.DormName), strings.TrimSpace(r.Content)
	if err := validateMultiDeductionRecord(r); err != nil {
		return nil, err
	}
	r.ID = multiRecordID(r)
	if _, err := db.Exec(`INSERT INTO police_style_records_multi_subrecords (id, submit_date, dorm_name, content, score) VALUES (?, ?, ?, ?, ?)`, r.ID, r.SubmitDate, r.DormName, r.Content, r.Score); err != nil {
		return nil, fmt.Errorf("创建寝室整体差记录失败: %w", err)
	}
	return &r, nil
}

func UpdateMultiDeductionRecord(db *sql.DB, id string, r MultiDeductionRecord) error {
	r.DormName, r.Content = strings.TrimSpace(r.DormName), strings.TrimSpace(r.Content)
	if r.DormName == "" {
		return errors.New("寝室名称不能为空")
	}
	if r.Content == "" {
		return errors.New("扣分项目不能为空")
	}
	if r.Score < 0 {
		return errors.New("分数不能为负数")
	}
	var exists int
	if err := db.QueryRow(`SELECT 1 FROM police_style_records_multi_subrecords WHERE id=?`, id).Scan(&exists); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return errors.New("寝室整体差记录不存在")
		}
		return err
	}
	result, err := db.Exec(`UPDATE police_style_records_multi_subrecords SET dorm_name=?,content=?,score=? WHERE id=?`, r.DormName, r.Content, r.Score, id)
	if err != nil {
		return err
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return errors.New("寝室整体差记录不存在")
	}
	return nil
}
func DeleteMultiDeductionRecords(db *sql.DB, ids []string) (int, error) {
	tx, err := db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	n := 0
	for _, id := range ids {
		if _, err = tx.Exec(`DELETE FROM ownership_multi_subrecords WHERE subrecord_id IN (SELECT id FROM subrecords_for_police_style_records_multi_subrecords WHERE belongs_to=?)`, id); err != nil {
			return 0, err
		}
		if _, err = tx.Exec(`DELETE FROM subrecords_for_police_style_records_multi_subrecords WHERE belongs_to=?`, id); err != nil {
			return 0, err
		}
		res, err := tx.Exec(`DELETE FROM police_style_records_multi_subrecords WHERE id=?`, id)
		if err != nil {
			return 0, err
		}
		a, _ := res.RowsAffected()
		n += int(a)
	}
	return n, tx.Commit()
}
func validateMultiDeductionRecord(r MultiDeductionRecord) error {
	if r.DormName == "" {
		return errors.New("寝室名称不能为空")
	}
	if r.Content == "" {
		return errors.New("扣分项目不能为空")
	}
	if _, err := time.Parse("2006-01-02 15:04:05", r.SubmitDate); err != nil {
		return errors.New("日期格式必须为 YYYY-MM-DD HH:MM:SS")
	}
	if r.Score < 0 {
		return errors.New("分数不能为负数")
	}
	return nil
}

func ListMultiDeductionSubrecords(db *sql.DB, recordID string) ([]MultiDeductionSubrecord, error) {
	rows, err := db.Query(`SELECT s.id, s.belongs_to, s.content, COALESCE(GROUP_CONCAT(o.student_id, ','), ''), COALESCE(GROUP_CONCAT(st.stu_name, ', '), '') FROM subrecords_for_police_style_records_multi_subrecords s LEFT JOIN ownership_multi_subrecords o ON o.subrecord_id=s.id LEFT JOIN students st ON st.id=o.student_id WHERE s.belongs_to=? GROUP BY s.id, s.belongs_to, s.content ORDER BY s.id`, recordID)
	if err != nil {
		return nil, fmt.Errorf("查询子项失败: %w", err)
	}
	defer rows.Close()
	result := make([]MultiDeductionSubrecord, 0)
	for rows.Next() {
		var s MultiDeductionSubrecord
		var ids string
		if err := rows.Scan(&s.ID, &s.BelongsTo, &s.Content, &ids, &s.StudentNames); err != nil {
			return nil, err
		}
		s.StudentIDs = splitStudentIDs(ids)
		result = append(result, s)
	}
	return result, rows.Err()
}

func SaveMultiDeductionSubrecord(db *sql.DB, sub MultiDeductionSubrecord) (*MultiDeductionSubrecord, error) {
	sub.Content = strings.TrimSpace(sub.Content)
	sub.StudentIDs = splitStudentIDs(strings.Join(sub.StudentIDs, ","))
	if sub.Content == "" {
		return nil, errors.New("子项内容不能为空")
	}
	if len(sub.StudentIDs) == 0 {
		return nil, errors.New("请指定至少一名负责同学")
	}
	tx, err := db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	for _, studentID := range sub.StudentIDs {
		var n int
		if err := tx.QueryRow(`SELECT COUNT(*) FROM students WHERE id=?`, studentID).Scan(&n); err != nil || n == 0 {
			return nil, fmt.Errorf("学号 %q 不存在", studentID)
		}
	}
	if sub.ID == "" {
		sub.ID = multiSubrecordID(sub)
		if _, err := tx.Exec(`INSERT INTO subrecords_for_police_style_records_multi_subrecords (id, belongs_to, content) VALUES (?, ?, ?)`, sub.ID, sub.BelongsTo, sub.Content); err != nil {
			return nil, err
		}
	} else if _, err := tx.Exec(`UPDATE subrecords_for_police_style_records_multi_subrecords SET content=? WHERE id=? AND belongs_to=?`, sub.Content, sub.ID, sub.BelongsTo); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(`DELETE FROM ownership_multi_subrecords WHERE subrecord_id=?`, sub.ID); err != nil {
		return nil, err
	}
	for _, studentID := range sub.StudentIDs {
		if _, err := tx.Exec(`INSERT INTO ownership_multi_subrecords (subrecord_id, student_id) VALUES (?, ?)`, sub.ID, studentID); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &sub, nil
}

func DeleteMultiDeductionSubrecord(db *sql.DB, id string) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.Exec(`DELETE FROM ownership_multi_subrecords WHERE subrecord_id=?`, id); err != nil {
		return err
	}
	if _, err = tx.Exec(`DELETE FROM subrecords_for_police_style_records_multi_subrecords WHERE id=?`, id); err != nil {
		return err
	}
	return tx.Commit()
}
func multiRecordID(r MultiDeductionRecord) string {
	raw := fmt.Sprintf("%s\x1f%s\x1f%s\x1f%g", r.SubmitDate, r.DormName, r.Content, r.Score)
	return "xd_" + fmt.Sprintf("%x", md5.Sum([]byte(raw)))[3:]
}
func multiSubrecordID(s MultiDeductionSubrecord) string {
	raw := s.BelongsTo + "\x1f" + s.Content + "\x1f" + strings.Join(s.StudentIDs, ",")
	return "xd_s_" + fmt.Sprintf("%x", md5.Sum([]byte(raw)))[5:]
}
