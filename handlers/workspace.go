package handlers

import (
	"bytes"
	"embed"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"time"

	"PoliceStyleWorkspace/models"
	"github.com/xuri/excelize/v2"
)

//go:embed embedded/daily_export_template.xlsx
var dailyExportTemplate embed.FS

type workspaceRecords struct {
	Single []models.DeductionRecord      `json:"single"`
	Multi  []models.MultiDeductionRecord `json:"multi"`
}

type dailyWeek struct {
	Index int      `json:"index"`
	Start string   `json:"start"`
	End   string   `json:"end"`
	Dates []string `json:"dates"`
}

type dailyStudentRow struct {
	ID     string             `json:"id"`
	Name   string             `json:"name"`
	Scores map[string]float64 `json:"scores"`
}

type dailySummaryRow struct {
	ID     string             `json:"id"`
	Name   string             `json:"name"`
	Scores map[string]float64 `json:"scores"`
	Total  float64            `json:"total"`
}

func (a *App) DailyManagementSummary(w http.ResponseWriter, r *http.Request) {
	semester, err := models.GetSemester(a.DB, r.PathValue("name"))
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	start, end, ok := semesterRange(*semester)
	if !ok {
		writeError(w, http.StatusBadRequest, "学期日期无效")
		return
	}
	students, err := models.ListStudents(a.DB)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	weeks := make([]dailyWeek, 0)
	rows := make([]dailySummaryRow, 0, len(students))
	for _, student := range students {
		rows = append(rows, dailySummaryRow{ID: student.ID, Name: student.Name, Scores: make(map[string]float64)})
	}
	for index, weekStart := 0, start; weekStart.Before(end); index, weekStart = index+1, weekStart.AddDate(0, 0, 7) {
		weekEnd := weekStart.AddDate(0, 0, 7)
		if weekEnd.After(end) {
			weekEnd = end
		}
		weeks = append(weeks, makeDailyWeek(index, weekStart, weekEnd))
		byID := make(map[string]*dailyStudentRow, len(students))
		for _, student := range students {
			row := dailyStudentRow{ID: student.ID, Name: student.Name, Scores: make(map[string]float64)}
			byID[student.ID] = &row
		}
		if err := a.fillDailyScores(byID, weekStart, weekEnd); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		for i, student := range students {
			var total float64
			for _, score := range byID[student.ID].Scores {
				total += score
			}
			rows[i].Scores[fmt.Sprintf("week_%d", index)] = total
			rows[i].Total += total
		}
	}
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].Total > rows[j].Total })
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "weeks": weeks, "rows": rows})
}

func (a *App) DailyManagementWeek(w http.ResponseWriter, r *http.Request) {
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
	week := makeDailyWeek(weekIndex, weekStart, weekEnd)
	students, err := models.ListStudents(a.DB)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	rows := make([]dailyStudentRow, 0, len(students))
	byID := make(map[string]*dailyStudentRow, len(students))
	for _, student := range students {
		row := dailyStudentRow{ID: student.ID, Name: student.Name, Scores: make(map[string]float64)}
		rows = append(rows, row)
		byID[student.ID] = &rows[len(rows)-1]
	}
	if err := a.fillDailyScores(byID, weekStart, weekEnd); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "week": week, "rows": rows})
}

func (a *App) ExportDailyManagementWeek(w http.ResponseWriter, r *http.Request) {
	semester, err := models.GetSemester(a.DB, r.PathValue("name"))
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	index, err := strconv.Atoi(r.PathValue("week"))
	if err != nil || index < 0 {
		writeError(w, http.StatusBadRequest, "周序号无效")
		return
	}
	start, end, ok := semesterRange(*semester)
	if !ok {
		writeError(w, 400, "学期日期无效")
		return
	}
	weekStart := start.AddDate(0, 0, index*7)
	if !weekStart.Before(end) {
		writeError(w, 400, "周序号超出学期范围")
		return
	}
	weekEnd := weekStart.AddDate(0, 0, 7)
	if weekEnd.After(end) {
		weekEnd = end
	}
	week, rows, err := a.dailyWeekRows(weekStart, weekEnd)
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	var order []string
	if raw := r.URL.Query().Get("order"); raw != "" {
		_ = json.Unmarshal([]byte(raw), &order)
	}
	rows = reorderDailyRows(rows, order)
	file, err := openDailyExportTemplate()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "读取导出模板失败: "+err.Error())
		return
	}
	sheet := file.GetSheetName(0)
	headerStyle, _ := file.GetCellStyle(sheet, "H1")
	bodyStyle, _ := file.GetCellStyle(sheet, "H2")
	headerTextStyle := headerStyle
	if style, err := file.GetStyle(headerStyle); err == nil {
		style.NumFmt = 49
		numFmt := "@"
		style.CustomNumFmt = &numFmt
		if id, err := file.NewStyle(style); err == nil {
			headerTextStyle = id
		}
	}
	decimalStyle := bodyStyle
	if style, err := file.GetStyle(bodyStyle); err == nil {
		style.NumFmt = 0
		numFmt := "0.0#####"
		style.CustomNumFmt = &numFmt
		if id, err := file.NewStyle(style); err == nil {
			decimalStyle = id
		}
	}
	if len(week.Dates)+3 > 8 {
		lastCol, _ := excelize.ColumnNumberToName(len(week.Dates) + 3)
		_ = file.SetCellStyle(sheet, fmt.Sprintf("H1"), fmt.Sprintf("%s1", lastCol), headerStyle)
		_ = file.SetCellStyle(sheet, "H2", fmt.Sprintf("%s2", lastCol), bodyStyle)
		_ = file.SetColWidth(sheet, "H", lastCol, 12.27)
	}
	file.SetCellValue(sheet, "A1", "姓名")
	file.SetCellValue(sheet, "B1", "学号")
	for i, date := range week.Dates {
		col, _ := excelize.ColumnNumberToName(i + 3)
		_ = file.SetCellStyle(sheet, fmt.Sprintf("%s1", col), fmt.Sprintf("%s1", col), headerTextStyle)
		file.SetCellValue(sheet, fmt.Sprintf("%s1", col), date)
	}
	totalCol, _ := excelize.ColumnNumberToName(len(week.Dates) + 3)
	file.SetCellValue(sheet, fmt.Sprintf("%s1", totalCol), "个人合计")
	for rowIndex, row := range rows {
		excelRow := rowIndex + 2
		_ = file.SetCellStyle(sheet, fmt.Sprintf("A%d", excelRow), fmt.Sprintf("%s%d", totalCol, excelRow), bodyStyle)
		file.SetCellValue(sheet, fmt.Sprintf("A%d", excelRow), row.Name)
		file.SetCellValue(sheet, fmt.Sprintf("B%d", excelRow), row.ID)
		total := 0.0
		for i, date := range week.Dates {
			score := row.Scores[date]
			total += score
			col, _ := excelize.ColumnNumberToName(i + 3)
			_ = file.SetCellStyle(sheet, fmt.Sprintf("%s%d", col, excelRow), fmt.Sprintf("%s%d", col, excelRow), decimalStyle)
			file.SetCellValue(sheet, fmt.Sprintf("%s%d", col, excelRow), score)
		}
		_ = file.SetCellStyle(sheet, fmt.Sprintf("%s%d", totalCol, excelRow), fmt.Sprintf("%s%d", totalCol, excelRow), decimalStyle)
		file.SetCellValue(sheet, fmt.Sprintf("%s%d", totalCol, excelRow), total)
	}
	// Bottom row contains per-day totals and the complete total for the week.
	totalRow := len(rows) + 2
	_ = file.SetCellStyle(sheet, fmt.Sprintf("A%d", totalRow), fmt.Sprintf("%s%d", totalCol, totalRow), bodyStyle)
	file.SetCellValue(sheet, fmt.Sprintf("A%d", totalRow), "总计")
	file.SetCellValue(sheet, fmt.Sprintf("B%d", totalRow), "")
	weekTotal := 0.0
	for i, date := range week.Dates {
		dayTotal := 0.0
		for _, row := range rows {
			dayTotal += row.Scores[date]
		}
		weekTotal += dayTotal
		col, _ := excelize.ColumnNumberToName(i + 3)
		_ = file.SetCellStyle(sheet, fmt.Sprintf("%s%d", col, totalRow), fmt.Sprintf("%s%d", col, totalRow), decimalStyle)
		file.SetCellValue(sheet, fmt.Sprintf("%s%d", col, totalRow), dayTotal)
	}
	_ = file.SetCellStyle(sheet, fmt.Sprintf("%s%d", totalCol, totalRow), fmt.Sprintf("%s%d", totalCol, totalRow), decimalStyle)
	file.SetCellValue(sheet, fmt.Sprintf("%s%d", totalCol, totalRow), weekTotal)
	if err := a.fillDailyExportDetails(file, weekStart, weekEnd); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.writeXLSX(w, file, fmt.Sprintf("%s-第%d周扣分.xlsx", semester.Name, index+1))
}

func reorderDailyRows(rows []dailyStudentRow, order []string) []dailyStudentRow {
	if len(order) == 0 {
		return rows
	}
	byID := make(map[string]dailyStudentRow, len(rows))
	for _, row := range rows {
		byID[row.ID] = row
	}
	result := make([]dailyStudentRow, 0, len(rows))
	seen := make(map[string]bool)
	for _, id := range order {
		if row, ok := byID[id]; ok && !seen[id] {
			result = append(result, row)
			seen[id] = true
		}
	}
	for _, row := range rows {
		if !seen[row.ID] {
			result = append(result, row)
		}
	}
	return result
}

func (a *App) dailyExportPreferencePath(name string) string {
	if a.ConfigDir == "" {
		return ""
	}
	return filepath.Join(a.ConfigDir, "daily_export_order_"+url.PathEscape(name)+".json")
}

func (a *App) GetDailyExportPreferences(w http.ResponseWriter, r *http.Request) {
	path := a.dailyExportPreferencePath(r.PathValue("name"))
	var order []string
	if path != "" {
		if data, err := os.ReadFile(path); err == nil {
			_ = json.Unmarshal(data, &order)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "order": order})
}

func (a *App) SaveDailyExportPreferences(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Order []string `json:"order"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	path := a.dailyExportPreferencePath(r.PathValue("name"))
	if path == "" {
		writeError(w, 500, "配置目录不可用")
		return
	}
	if err := os.MkdirAll(a.ConfigDir, 0755); err != nil {
		writeError(w, 500, err.Error())
		return
	}
	data, _ := json.MarshalIndent(req.Order, "", "  ")
	if err := os.WriteFile(path, data, 0644); err != nil {
		writeError(w, 500, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (a *App) fillDailyExportDetails(file *excelize.File, start, end time.Time) error {
	sheet := file.GetSheetName(1)
	detailStyle, _ := file.GetCellStyle(sheet, "A2")
	detailTextStyle := detailStyle
	if style, err := file.GetStyle(detailStyle); err == nil {
		style.NumFmt = 49
		numFmt := "@"
		style.CustomNumFmt = &numFmt
		if id, err := file.NewStyle(style); err == nil {
			detailTextStyle = id
		}
	}
	detailDecimalStyle := detailStyle
	if style, err := file.GetStyle(detailStyle); err == nil {
		style.NumFmt = 0
		numFmt := "0.0#####"
		style.CustomNumFmt = &numFmt
		if id, err := file.NewStyle(style); err == nil {
			detailDecimalStyle = id
		}
	}
	students, err := models.ListStudents(a.DB)
	if err != nil {
		return err
	}
	names := make(map[string]string, len(students))
	for _, student := range students {
		names[student.ID] = student.Name
	}
	row := 2
	single, err := models.ListDeductionRecords(a.DB)
	if err != nil {
		return err
	}
	for _, record := range single {
		date, ok := recordDate(record.SubmitDate)
		if !ok || date.Before(start) || !date.Before(end) || len(record.RecognizedStudentIDs) == 0 {
			continue
		}
		share := record.Score / float64(len(record.RecognizedStudentIDs))
		for _, studentID := range record.RecognizedStudentIDs {
			_ = file.SetCellStyle(sheet, fmt.Sprintf("A%d", row), fmt.Sprintf("C%d", row), detailTextStyle)
			_ = file.SetCellStyle(sheet, fmt.Sprintf("D%d", row), fmt.Sprintf("D%d", row), detailDecimalStyle)
			file.SetCellValue(sheet, fmt.Sprintf("A%d", row), date.Format("2006-01-02"))
			file.SetCellValue(sheet, fmt.Sprintf("B%d", row), names[studentID])
			file.SetCellValue(sheet, fmt.Sprintf("C%d", row), record.Content)
			file.SetCellValue(sheet, fmt.Sprintf("D%d", row), share)
			row++
		}
	}
	multi, err := models.ListMultiDeductionRecords(a.DB)
	if err != nil {
		return err
	}
	for _, record := range multi {
		date, ok := recordDate(record.SubmitDate)
		if !ok || date.Before(start) || !date.Before(end) {
			continue
		}
		subs, err := models.ListMultiDeductionSubrecords(a.DB, record.ID)
		if err != nil {
			return err
		}
		if len(subs) == 0 {
			continue
		}
		for _, sub := range subs {
			if len(sub.StudentIDs) == 0 {
				continue
			}
			share := record.Score / float64(len(subs)) / float64(len(sub.StudentIDs))
			for _, studentID := range sub.StudentIDs {
				_ = file.SetCellStyle(sheet, fmt.Sprintf("A%d", row), fmt.Sprintf("C%d", row), detailTextStyle)
				_ = file.SetCellStyle(sheet, fmt.Sprintf("D%d", row), fmt.Sprintf("D%d", row), detailDecimalStyle)
				file.SetCellValue(sheet, fmt.Sprintf("A%d", row), date.Format("2006-01-02"))
				file.SetCellValue(sheet, fmt.Sprintf("B%d", row), names[studentID])
				file.SetCellValue(sheet, fmt.Sprintf("C%d", row), sub.Content)
				file.SetCellValue(sheet, fmt.Sprintf("D%d", row), share)
				row++
			}
		}
	}
	return nil
}

func openDailyExportTemplate() (*excelize.File, error) {
	fileBytes, err := dailyExportTemplate.ReadFile("embedded/daily_export_template.xlsx")
	if err != nil {
		return nil, err
	}
	return excelize.OpenReader(bytes.NewReader(fileBytes))
}

func (a *App) ExportDailyManagementSummary(w http.ResponseWriter, r *http.Request) {
	semester, err := models.GetSemester(a.DB, r.PathValue("name"))
	if err != nil {
		writeError(w, 404, err.Error())
		return
	}
	start, end, ok := semesterRange(*semester)
	if !ok {
		writeError(w, 400, "学期日期无效")
		return
	}
	students, err := models.ListStudents(a.DB)
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	weeks := make([]dailyWeek, 0)
	rows := make([]dailySummaryRow, 0, len(students))
	for _, student := range students {
		rows = append(rows, dailySummaryRow{ID: student.ID, Name: student.Name, Scores: map[string]float64{}})
	}
	for index, weekStart := 0, start; weekStart.Before(end); index, weekStart = index+1, weekStart.AddDate(0, 0, 7) {
		weekEnd := weekStart.AddDate(0, 0, 7)
		if weekEnd.After(end) {
			weekEnd = end
		}
		weeks = append(weeks, makeDailyWeek(index, weekStart, weekEnd))
		byID := map[string]*dailyStudentRow{}
		for _, student := range students {
			byID[student.ID] = &dailyStudentRow{ID: student.ID, Name: student.Name, Scores: map[string]float64{}}
		}
		if err := a.fillDailyScores(byID, weekStart, weekEnd); err != nil {
			writeError(w, 500, err.Error())
			return
		}
		for i, student := range students {
			total := 0.0
			for _, score := range byID[student.ID].Scores {
				total += score
			}
			rows[i].Scores[fmt.Sprintf("week_%d", index)] = total
			rows[i].Total += total
		}
	}
	file := excelize.NewFile()
	sheet := file.GetSheetName(0)
	file.SetCellValue(sheet, "A1", "学号")
	file.SetCellValue(sheet, "B1", "姓名")
	file.SetCellValue(sheet, "C1", "总扣分")
	for i, week := range weeks {
		col, _ := excelize.ColumnNumberToName(i + 4)
		file.SetCellValue(sheet, fmt.Sprintf("%s1", col), fmt.Sprintf("第%d周 (%s~%s)", i+1, week.Start, week.End))
	}
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].Total > rows[j].Total })
	for rowIndex, row := range rows {
		excelRow := rowIndex + 2
		file.SetCellValue(sheet, fmt.Sprintf("A%d", excelRow), row.ID)
		file.SetCellValue(sheet, fmt.Sprintf("B%d", excelRow), row.Name)
		file.SetCellValue(sheet, fmt.Sprintf("C%d", excelRow), row.Total)
		for i := range weeks {
			col, _ := excelize.ColumnNumberToName(i + 4)
			file.SetCellValue(sheet, fmt.Sprintf("%s%d", col, excelRow), row.Scores[fmt.Sprintf("week_%d", i)])
		}
	}
	a.writeXLSX(w, file, fmt.Sprintf("%s-学期汇总.xlsx", semester.Name))
}

func (a *App) dailyWeekRows(start, end time.Time) (dailyWeek, []dailyStudentRow, error) {
	students, err := models.ListStudents(a.DB)
	if err != nil {
		return dailyWeek{}, nil, err
	}
	week := makeDailyWeek(0, start, end)
	rows := make([]dailyStudentRow, 0, len(students))
	byID := map[string]*dailyStudentRow{}
	for _, student := range students {
		rows = append(rows, dailyStudentRow{ID: student.ID, Name: student.Name, Scores: map[string]float64{}})
		byID[student.ID] = &rows[len(rows)-1]
	}
	if err := a.fillDailyScores(byID, start, end); err != nil {
		return dailyWeek{}, nil, err
	}
	return week, rows, nil
}

func (a *App) writeXLSX(w http.ResponseWriter, file *excelize.File, name string) {
	defer file.Close()
	w.Header().Set("Content-Type", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet")
	w.Header().Set("Content-Disposition", `attachment; filename*=UTF-8''`+url.QueryEscape(name))
	if err := file.Write(w); err != nil {
		return
	}
}

func makeDailyWeek(index int, start, end time.Time) dailyWeek {
	dates := make([]string, 0, 7)
	for day := start; day.Before(end); day = day.AddDate(0, 0, 1) {
		dates = append(dates, day.Format("2006-01-02"))
	}
	return dailyWeek{Index: index, Start: start.Format("2006-01-02"), End: end.AddDate(0, 0, -1).Format("2006-01-02"), Dates: dates}
}

func (a *App) fillDailyScores(rows map[string]*dailyStudentRow, start, end time.Time) error {
	query := `SELECT student_id, day, SUM(score) FROM (
		SELECT o.student_id AS student_id, substr(r.submit_date, 1, 10) AS day,
			r.score * 1.0 / ownership_counts.student_count AS score
		FROM police_style_records_single_subrecords r
		JOIN ownership_single_subrecords o ON o.record_id = r.id
		JOIN (SELECT record_id, COUNT(*) AS student_count FROM ownership_single_subrecords GROUP BY record_id) ownership_counts
			ON ownership_counts.record_id = r.id
		WHERE r.submit_date >= ? AND r.submit_date < ?
		UNION ALL
		SELECT o.student_id AS student_id, substr(r.submit_date, 1, 10) AS day,
			r.score * 1.0 / sub_counts.sub_count / student_counts.student_count AS score
		FROM police_style_records_multi_subrecords r
		JOIN subrecords_for_police_style_records_multi_subrecords s ON s.belongs_to = r.id
		JOIN ownership_multi_subrecords o ON o.subrecord_id = s.id
		JOIN (SELECT belongs_to, COUNT(*) AS sub_count FROM subrecords_for_police_style_records_multi_subrecords GROUP BY belongs_to) sub_counts
			ON sub_counts.belongs_to = r.id
		JOIN (SELECT subrecord_id, COUNT(*) AS student_count FROM ownership_multi_subrecords GROUP BY subrecord_id) student_counts
			ON student_counts.subrecord_id = s.id
		WHERE r.submit_date >= ? AND r.submit_date < ?
	) GROUP BY student_id, day`
	rowsResult, err := a.DB.Query(query, start.Format("2006-01-02 15:04:05"), end.Format("2006-01-02 15:04:05"), start.Format("2006-01-02 15:04:05"), end.Format("2006-01-02 15:04:05"))
	if err != nil {
		return fmt.Errorf("统计周扣分失败: %w", err)
	}
	defer rowsResult.Close()
	for rowsResult.Next() {
		var studentID, day string
		var score float64
		if err := rowsResult.Scan(&studentID, &day, &score); err != nil {
			return fmt.Errorf("读取周扣分失败: %w", err)
		}
		if row := rows[studentID]; row != nil {
			row.Scores[day] = score
		}
	}
	return rowsResult.Err()
}

func (a *App) WorkspaceStats(w http.ResponseWriter, r *http.Request) {
	stats := struct {
		SingleDeductionCount        int     `json:"single_deduction_count"`
		MultiDeductionCount         int     `json:"multi_deduction_count"`
		MultiWithoutSubrecordsCount int     `json:"multi_without_subrecords_count"`
		TotalScore                  float64 `json:"total_score"`
		UnassignedCount             int     `json:"unassigned_count"`
		OutOfSemesterCount          int     `json:"out_of_semester_count"`
		DutyDormName                string  `json:"duty_dorm_name"`
	}{}

	if err := a.DB.QueryRow(`SELECT COUNT(*) FROM police_style_records_single_subrecords`).Scan(&stats.SingleDeductionCount); err != nil {
		writeError(w, http.StatusInternalServerError, "统计常规扣分失败: "+err.Error())
		return
	}
	if err := a.DB.QueryRow(`SELECT COUNT(*) FROM police_style_records_multi_subrecords`).Scan(&stats.MultiDeductionCount); err != nil {
		writeError(w, http.StatusInternalServerError, "统计寝室整体差扣分失败: "+err.Error())
		return
	}
	if err := a.DB.QueryRow(`SELECT COUNT(*) FROM police_style_records_multi_subrecords r WHERE NOT EXISTS (
		SELECT 1 FROM subrecords_for_police_style_records_multi_subrecords s WHERE s.belongs_to = r.id
	)`).Scan(&stats.MultiWithoutSubrecordsCount); err != nil {
		writeError(w, http.StatusInternalServerError, "统计无子项寝室整体差失败: "+err.Error())
		return
	}
	if err := a.DB.QueryRow(`SELECT COALESCE((SELECT SUM(score) FROM police_style_records_single_subrecords), 0) + COALESCE((SELECT SUM(score) FROM police_style_records_multi_subrecords), 0)`).Scan(&stats.TotalScore); err != nil {
		writeError(w, http.StatusInternalServerError, "统计总扣分失败: "+err.Error())
		return
	}
	if err := a.DB.QueryRow(`SELECT
		(SELECT COUNT(*) FROM police_style_records_single_subrecords r WHERE NOT EXISTS (SELECT 1 FROM ownership_single_subrecords o WHERE o.record_id = r.id)) +
		(SELECT COUNT(*) FROM police_style_records_multi_subrecords r WHERE EXISTS (
			SELECT 1
			FROM subrecords_for_police_style_records_multi_subrecords s
			WHERE s.belongs_to = r.id
				AND NOT EXISTS (SELECT 1 FROM ownership_multi_subrecords o WHERE o.subrecord_id = s.id)
		))`).Scan(&stats.UnassignedCount); err != nil {
		writeError(w, http.StatusInternalServerError, "统计未指定项目失败: "+err.Error())
		return
	}
	if err := a.setCurrentDutyDorm(&stats); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	outOfSemester, err := a.workspaceRecords(func(date time.Time, semesters []models.Semester) bool {
		for _, semester := range semesters {
			start, end, ok := semesterRange(semester)
			if ok && !date.Before(start) && date.Before(end) {
				return false
			}
		}
		return true
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	stats.OutOfSemesterCount = len(outOfSemester.Single) + len(outOfSemester.Multi)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "stats": stats})
}

func (a *App) ListOutOfSemesterRecords(w http.ResponseWriter, r *http.Request) {
	records, err := a.workspaceRecords(func(date time.Time, semesters []models.Semester) bool {
		for _, semester := range semesters {
			start, end, ok := semesterRange(semester)
			if ok && !date.Before(start) && date.Before(end) {
				return false
			}
		}
		return true
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "records": records})
}

func (a *App) ListMultiWithoutSubrecordsWorkspaceRecords(w http.ResponseWriter, r *http.Request) {
	multi, err := a.multiRecordsWithoutSubrecords()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok": true,
		"records": workspaceRecords{
			Single: []models.DeductionRecord{},
			Multi:  multi,
		},
	})
}

func (a *App) ListUnassignedWorkspaceRecords(w http.ResponseWriter, r *http.Request) {
	single, err := models.ListDeductionRecords(a.DB)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	multi, err := a.multiRecordsWithUnassignedSubrecords()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	filtered := make([]models.DeductionRecord, 0)
	for _, record := range single {
		if len(record.RecognizedStudentIDs) == 0 {
			filtered = append(filtered, record)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "records": workspaceRecords{Single: filtered, Multi: multi}})
}

func (a *App) workspaceRecords(include func(time.Time, []models.Semester) bool) (workspaceRecords, error) {
	semesters, err := models.ListSemesters(a.DB)
	if err != nil {
		return workspaceRecords{}, err
	}
	single, err := models.ListDeductionRecords(a.DB)
	if err != nil {
		return workspaceRecords{}, err
	}
	multi, err := models.ListMultiDeductionRecords(a.DB)
	if err != nil {
		return workspaceRecords{}, err
	}
	result := workspaceRecords{Single: make([]models.DeductionRecord, 0), Multi: make([]models.MultiDeductionRecord, 0)}
	for _, record := range single {
		if date, ok := recordDate(record.SubmitDate); ok && include(date, semesters) {
			result.Single = append(result.Single, record)
		}
	}
	for _, record := range multi {
		if date, ok := recordDate(record.SubmitDate); ok && include(date, semesters) {
			result.Multi = append(result.Multi, record)
		}
	}
	return result, nil
}

func (a *App) multiRecordsWithoutSubrecords() ([]models.MultiDeductionRecord, error) {
	all, err := models.ListMultiDeductionRecords(a.DB)
	if err != nil {
		return nil, err
	}
	result := make([]models.MultiDeductionRecord, 0)
	for _, record := range all {
		subs, err := models.ListMultiDeductionSubrecords(a.DB, record.ID)
		if err != nil {
			return nil, err
		}
		if len(subs) == 0 {
			result = append(result, record)
		}
	}
	return result, nil
}

func (a *App) multiRecordsWithUnassignedSubrecords() ([]models.MultiDeductionRecord, error) {
	all, err := models.ListMultiDeductionRecords(a.DB)
	if err != nil {
		return nil, err
	}
	result := make([]models.MultiDeductionRecord, 0)
	for _, record := range all {
		subs, err := models.ListMultiDeductionSubrecords(a.DB, record.ID)
		if err != nil {
			return nil, err
		}
		for _, sub := range subs {
			if len(sub.StudentIDs) == 0 {
				result = append(result, record)
				break
			}
		}
	}
	return result, nil
}

func semesterRange(semester models.Semester) (time.Time, time.Time, bool) {
	start, startErr := time.ParseInLocation("2006-01-02", semester.StartTime, time.Local)
	end, endErr := time.ParseInLocation("2006-01-02", semester.EndTime, time.Local)
	return start, end.AddDate(0, 0, 1), startErr == nil && endErr == nil
}

func recordDate(value string) (time.Time, bool) {
	date, err := time.ParseInLocation("2006-01-02 15:04:05", value, time.Local)
	if err != nil {
		return time.Time{}, false
	}
	return date, true
}

func (a *App) setCurrentDutyDorm(stats *struct {
	SingleDeductionCount        int     `json:"single_deduction_count"`
	MultiDeductionCount         int     `json:"multi_deduction_count"`
	MultiWithoutSubrecordsCount int     `json:"multi_without_subrecords_count"`
	TotalScore                  float64 `json:"total_score"`
	UnassignedCount             int     `json:"unassigned_count"`
	OutOfSemesterCount          int     `json:"out_of_semester_count"`
	DutyDormName                string  `json:"duty_dorm_name"`
}) error {
	semesters, err := models.ListSemesters(a.DB)
	if err != nil {
		return err
	}
	now := time.Now()
	for _, semester := range semesters {
		start, startErr := time.ParseInLocation("2006-01-02", semester.StartTime, time.Local)
		end, endErr := time.ParseInLocation("2006-01-02", semester.EndTime, time.Local)
		if startErr != nil || endErr != nil {
			continue
		}
		end = end.AddDate(0, 0, 1)
		if now.Before(start) || !now.Before(end) {
			continue
		}
		dorms, err := models.ListDorms(a.DB)
		if err != nil {
			return err
		}
		if len(dorms) == 0 {
			return nil
		}
		weekIndex := int(now.Sub(start).Hours() / (24 * 7))
		dutySeq := weekIndex%len(dorms) + 1
		for _, dorm := range dorms {
			if dorm.Seq == dutySeq {
				stats.DutyDormName = dorm.Name
				break
			}
		}
		return nil
	}
	return nil
}
