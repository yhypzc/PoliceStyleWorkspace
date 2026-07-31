package models

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

type Semester struct {
	Name      string `json:"semester_name"`
	StartTime string `json:"start_time"`
	EndTime   string `json:"end_time"`
}

// Validate returns an error if the semester dates are not Saturday→next Friday.
func (s *Semester) Validate() error {
	if strings.TrimSpace(s.Name) == "" {
		return errors.New("学期名称不能为空")
	}
	start, err := time.Parse("2006-01-02", s.StartTime)
	if err != nil {
		return fmt.Errorf("起始日期格式错误: %w", err)
	}
	end, err := time.Parse("2006-01-02", s.EndTime)
	if err != nil {
		return fmt.Errorf("结束日期格式错误: %w", err)
	}
	if start.Weekday() != time.Saturday {
		return errors.New("起始日期必须是周六")
	}
	if end.Weekday() != time.Friday {
		return errors.New("结束日期必须是周五")
	}
	if !end.After(start) {
		return errors.New("结束日期必须在起始日期之后")
	}
	return nil
}

// overlaps returns true if two date ranges overlap.
func overlaps(aStart, aEnd, bStart, bEnd string) bool {
	return aStart <= bEnd && aEnd >= bStart
}

func checkSemesterOverlap(db *sql.DB, s Semester, excludeName string) error {
	list, err := ListSemesters(db)
	if err != nil {
		return err
	}
	for _, existing := range list {
		if existing.Name == excludeName {
			continue
		}
		if overlaps(s.StartTime, s.EndTime, existing.StartTime, existing.EndTime) {
			return fmt.Errorf("学期时段与已存在的学期 %q（%s ~ %s）重合", existing.Name, existing.StartTime, existing.EndTime)
		}
	}
	return nil
}

func CreateSemester(db *sql.DB, s Semester) error {
	if err := s.Validate(); err != nil {
		return err
	}
	if err := checkSemesterOverlap(db, s, ""); err != nil {
		return err
	}
	_, err := db.Exec(
		`INSERT INTO semester (semester_name, start_time, end_time) VALUES (?, ?, ?)`,
		s.Name, s.StartTime, s.EndTime,
	)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint") {
			return errors.New("学期名称已存在")
		}
		return fmt.Errorf("创建学期失败: %w", err)
	}
	return nil
}

func UpdateSemester(db *sql.DB, s Semester) error {
	if err := s.Validate(); err != nil {
		return err
	}
	if err := checkSemesterOverlap(db, s, s.Name); err != nil {
		return err
	}
	res, err := db.Exec(
		`UPDATE semester SET start_time = ?, end_time = ? WHERE semester_name = ?`,
		s.StartTime, s.EndTime, s.Name,
	)
	if err != nil {
		return fmt.Errorf("更新学期失败: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return errors.New("学期不存在")
	}
	return nil
}

func ListSemesters(db *sql.DB) ([]Semester, error) {
	rows, err := db.Query(`SELECT semester_name, start_time, end_time FROM semester ORDER BY start_time DESC`)
	if err != nil {
		return nil, fmt.Errorf("查询学期列表失败: %w", err)
	}
	defer rows.Close()
	var list []Semester
	for rows.Next() {
		var s Semester
		if err := rows.Scan(&s.Name, &s.StartTime, &s.EndTime); err != nil {
			return nil, fmt.Errorf("扫描学期数据失败: %w", err)
		}
		list = append(list, s)
	}
	return list, rows.Err()
}

func GetSemester(db *sql.DB, name string) (*Semester, error) {
	s := &Semester{}
	err := db.QueryRow(
		`SELECT semester_name, start_time, end_time FROM semester WHERE semester_name = ?`, name,
	).Scan(&s.Name, &s.StartTime, &s.EndTime)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("学期 %q 不存在", name)
	}
	if err != nil {
		return nil, fmt.Errorf("查询学期失败: %w", err)
	}
	return s, nil
}

func DeleteSemester(db *sql.DB, name string) error {
	res, err := db.Exec(`DELETE FROM semester WHERE semester_name = ?`, name)
	if err != nil {
		return fmt.Errorf("删除学期失败: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("学期 %q 不存在", name)
	}
	return nil
}
