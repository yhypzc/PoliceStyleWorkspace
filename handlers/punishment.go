package handlers

import (
	"fmt"
	"math"
	"net/http"
	"sort"
	"strconv"
	"time"

	"PoliceStyleWorkspace/models"
)

type punishmentRecord struct {
	RecordID   string  `json:"record_id"`
	Content    string  `json:"content"`
	Date       string  `json:"date"`
	StudentIDs int     `json:"student_count"`
	IsMulti    bool    `json:"is_multi"`
	RawScore   float64 `json:"raw_score"`
	LogicScore float64 `json:"logic_score"`
}

type punishmentEntry struct {
	StudentID   string             `json:"student_id"`
	StudentName string             `json:"student_name"`
	Total       float64            `json:"total"`
	Records     []punishmentRecord `json:"records"`
}

func (a *App) PunishmentList(w http.ResponseWriter, r *http.Request) {
	semester, err := models.GetSemester(a.DB, r.PathValue("name"))
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	weekIndex, err := strconv.Atoi(r.PathValue("week"))
	if err != nil || weekIndex < 0 {
		writeError(w, http.StatusBadRequest, "周序号无效")
		return
	}
	start, end, ok := semesterRange(*semester)
	if !ok {
		writeError(w, http.StatusBadRequest, "学期日期无效")
		return
	}
	weekStart := start.AddDate(0, 0, weekIndex*7)
	if !weekStart.Before(end) {
		writeError(w, http.StatusBadRequest, "周序号超出学期范围")
		return
	}
	weekEnd := weekStart.AddDate(0, 0, 7)
	if weekEnd.After(end) {
		weekEnd = end
	}

	entries, err := a.computePunishmentList(weekStart, weekEnd)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "entries": entries})
}

func (a *App) computePunishmentList(weekStart, weekEnd time.Time) ([]punishmentEntry, error) {
	return a.computePunishmentEntries(weekStart, weekEnd, 0.3)
}

func (a *App) computePunishmentEntries(weekStart, weekEnd time.Time, threshold float64) ([]punishmentEntry, error) {
	students, err := models.ListStudents(a.DB)
	if err != nil {
		return nil, err
	}
	studentNames := make(map[string]string, len(students))
	for _, s := range students {
		studentNames[s.ID] = s.Name
	}

	type accum struct {
		total   float64
		records []punishmentRecord
	}
	byStudent := make(map[string]*accum)

	singleRows, err := a.DB.Query(`
		SELECT r.id, r.score, r.content, r.submit_date, o.student_id,
			(SELECT COUNT(*) FROM ownership_single_subrecords WHERE record_id = r.id) AS student_count
		FROM police_style_records_single_subrecords r
		JOIN ownership_single_subrecords o ON o.record_id = r.id
		WHERE r.submit_date >= ? AND r.submit_date < ?
		ORDER BY r.id`, weekStart.Format("2006-01-02 15:04:05"), weekEnd.Format("2006-01-02 15:04:05"))
	if err != nil {
		return nil, fmt.Errorf("查询常规扣分记录失败: %w", err)
	}
	defer singleRows.Close()
	for singleRows.Next() {
		var recordID, content, date, studentID string
		var score float64
		var studentCount int
		if err := singleRows.Scan(&recordID, &score, &content, &date, &studentID, &studentCount); err != nil {
			return nil, fmt.Errorf("读取常规扣分记录失败: %w", err)
		}
		x := score / float64(studentCount)
		logicScore := logicScoreSingle(x, studentCount)
		acc := byStudent[studentID]
		if acc == nil {
			acc = &accum{}
			byStudent[studentID] = acc
		}
		acc.total += logicScore
		acc.records = append(acc.records, punishmentRecord{
			RecordID: recordID, Content: content,
			Date: date[:10],
			StudentIDs: studentCount, IsMulti: false,
			RawScore: math.Round(x*1000) / 1000,
			LogicScore: math.Round(logicScore*1000) / 1000,
		})
	}
	if err := singleRows.Err(); err != nil {
		return nil, err
	}

	multiRows, err := a.DB.Query(`
		SELECT r.id, r.score, r.content, r.submit_date, s.content, o.student_id,
			(SELECT COUNT(*) FROM subrecords_for_police_style_records_multi_subrecords WHERE belongs_to = r.id) AS sub_count,
			(SELECT COUNT(*) FROM ownership_multi_subrecords WHERE subrecord_id = s.id) AS student_count
		FROM police_style_records_multi_subrecords r
		JOIN subrecords_for_police_style_records_multi_subrecords s ON s.belongs_to = r.id
		JOIN ownership_multi_subrecords o ON o.subrecord_id = s.id
		WHERE r.submit_date >= ? AND r.submit_date < ?
		ORDER BY r.id`, weekStart.Format("2006-01-02 15:04:05"), weekEnd.Format("2006-01-02 15:04:05"))
	if err != nil {
		return nil, fmt.Errorf("查询寝室整体差记录失败: %w", err)
	}
	defer multiRows.Close()
	for multiRows.Next() {
		var recordID, content, date, subContent, studentID string
		var score float64
		var subCount, studentCount int
		if err := multiRows.Scan(&recordID, &score, &content, &date, &subContent, &studentID, &subCount, &studentCount); err != nil {
			return nil, fmt.Errorf("读取寝室整体差记录失败: %w", err)
		}
		x := score / float64(subCount) / float64(studentCount)
		logicScore := logicScoreMulti(x, studentCount)
		acc := byStudent[studentID]
		if acc == nil {
			acc = &accum{}
			byStudent[studentID] = acc
		}
		acc.total += logicScore
		acc.records = append(acc.records, punishmentRecord{
			RecordID: recordID, Content: content + "_" + subContent,
			Date: date[:10],
			StudentIDs: studentCount, IsMulti: true,
			RawScore: math.Round(x*1000) / 1000,
			LogicScore: math.Round(logicScore*1000) / 1000,
		})
	}
	if err := multiRows.Err(); err != nil {
		return nil, err
	}

	var result []punishmentEntry
	for studentID, acc := range byStudent {
		if acc.total >= threshold {
			sort.SliceStable(acc.records, func(i, j int) bool {
				return acc.records[i].RecordID < acc.records[j].RecordID
			})
			result = append(result, punishmentEntry{
				StudentID:   studentID,
				StudentName: studentNames[studentID],
				Total:       math.Round(acc.total*1000) / 1000,
				Records:     acc.records,
			})
		}
	}
	if result == nil {
		result = make([]punishmentEntry, 0)
	}

	// ── 微调阶段：对所有阈值生效，实际调整 LogicScore 与 Total ──
	if len(result) > 0 {
		finalResult := make([]punishmentEntry, 0, len(result))
		for ei := range result {
			entry := &result[ei]
			// 统计只有该学生一人负责的寝室整体差子项
			type soloItem struct {
				idx      int
				rawScore float64
			}
			var soloItems []soloItem
			for k, rec := range entry.Records {
				if rec.IsMulti && rec.StudentIDs == 1 {
					soloItems = append(soloItems, soloItem{idx: k, rawScore: rec.RawScore})
				}
			}
			count := len(soloItems)
			// >= 3 项 或 0 项：不作调整，直接保留
			if count >= 3 || count == 0 {
				if threshold < 0.3 || entry.Total >= threshold {
					finalResult = append(finalResult, *entry)
				}
				continue
			}
			// 按 rawScore 升序排列
			sort.SliceStable(soloItems, func(a, b int) bool {
				return soloItems[a].rawScore < soloItems[b].rawScore
			})
			adjusted := entry.Total
			if count == 1 {
				si := soloItems[0]
				rec := &entry.Records[si.idx]
				adjusted = adjusted - rec.LogicScore + rec.RawScore
				rec.LogicScore = math.Round(rec.RawScore*1000) / 1000
			} else { // count == 2
				// 先松绑 RawScore 较小的项
				si1 := soloItems[0]
				rec1 := &entry.Records[si1.idx]
				adjusted = adjusted - rec1.LogicScore + rec1.RawScore
				rec1.LogicScore = math.Round(rec1.RawScore*1000) / 1000
				if threshold == 0.3 && adjusted < 0.3 {
					continue
				}
				// 再松绑另一项
				si2 := soloItems[1]
				rec2 := &entry.Records[si2.idx]
				adjusted = adjusted - rec2.LogicScore + rec2.RawScore
				rec2.LogicScore = math.Round(rec2.RawScore*1000) / 1000
			}
			entry.Total = math.Round(adjusted*1000) / 1000
			if threshold < 0.3 || entry.Total >= threshold {
				finalResult = append(finalResult, *entry)
			}
		}
		result = finalResult
	}

	sort.SliceStable(result, func(i, j int) bool {
		return result[i].Total > result[j].Total
	})
	return result, nil
}

func logicScoreSingle(x float64, studentCount int) float64 {
	if studentCount > 1 {
		if x < 0.1 {
			return 0
		}
		return x
	}
	return x
}

func logicScoreMulti(x float64, studentCount int) float64 {
	if studentCount > 1 {
		if x < 0.1 {
			return 0
		}
		return x
	}
	if x < 0.1 {
		return 0.1
	}
	return x
}
