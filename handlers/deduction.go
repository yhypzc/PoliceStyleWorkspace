package handlers

import (
	"embed"
	"fmt"
	"io"
	"log"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"PoliceStyleWorkspace/models"

	"github.com/xuri/excelize/v2"
)

//go:embed embedded/deduction_template.xlsx
var deductionTemplate embed.FS

func (a *App) ListDeductionRecords(w http.ResponseWriter, r *http.Request) {
	records, err := models.ListDeductionRecords(a.DB)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "records": records})
}

func (a *App) CreateDeductionRecord(w http.ResponseWriter, r *http.Request) {
	var req models.DeductionRecord
	if !decodeJSON(w, r, &req) {
		return
	}
	rec, err := models.CreateDeductionRecord(a.DB, req)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	log.Printf("[扣分] 新增记录 %q (姓名: %s, 扣分: %.1f)", rec.ID, rec.StudentName, rec.Score)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "record": rec})
}

func (a *App) UpdateDeductionRecord(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "无效的记录ID")
		return
	}
	var req models.DeductionRecord
	if !decodeJSON(w, r, &req) {
		return
	}
	if err := models.UpdateDeductionRecord(a.DB, id, req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	log.Printf("[扣分] 更新 ID=%q", id)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (a *App) UpdateDeductionRecognition(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req struct {
		StudentIDs []string `json:"student_ids"`
	}
	if id == "" || !decodeJSON(w, r, &req) {
		return
	}
	if err := models.ReplaceDeductionRecordStudents(a.DB, id, req.StudentIDs); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (a *App) DeleteDeductionRecord(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "无效的记录ID")
		return
	}
	if err := models.DeleteDeductionRecord(a.DB, id); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	log.Printf("[扣分] 删除 ID=%q", id)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (a *App) BatchDeleteDeductionRecords(w http.ResponseWriter, r *http.Request) {
	var req struct {
		IDs []string `json:"ids"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	deleted, err := models.DeleteDeductionRecords(a.DB, req.IDs)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	log.Printf("[扣分] 批量删除 %d 条记录", deleted)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "deleted": deleted})
}

func (a *App) ImportDeductionRecords(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 50<<20)
	if err := r.ParseMultipartForm(50 << 20); err != nil {
		writeError(w, http.StatusBadRequest, "文件过大，请上传小于 50 MB 的文件")
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		writeError(w, http.StatusBadRequest, "请上传 Excel 文件")
		return
	}
	defer file.Close()

	if !strings.HasSuffix(strings.ToLower(header.Filename), ".xlsx") {
		writeError(w, http.StatusBadRequest, "仅支持 .xlsx 格式")
		return
	}

	f, err := excelize.OpenReader(file)
	if err != nil {
		writeError(w, http.StatusBadRequest, "无法读取 Excel 文件: "+err.Error())
		return
	}
	defer f.Close()

	sheetName := f.GetSheetName(0)
	if sheetName == "" {
		writeError(w, http.StatusBadRequest, "Excel 文件中没有工作表")
		return
	}
	rows, err := f.GetRows(sheetName)
	if err != nil {
		writeError(w, http.StatusBadRequest, "读取工作表失败")
		return
	}
	if len(rows) < 2 {
		writeError(w, http.StatusBadRequest, "Excel 文件中没有数据行（除表头外至少需要一行数据）")
		return
	}

	headerRowIndex, dateCol, nameCol, studentIDCol, contentCol, scoreCol := findDeductionHeader(rows)
	if headerRowIndex < 0 {
		writeError(w, http.StatusBadRequest, "表头必须包含「学号」列")
		return
	}

	var imported []models.DeductionRecord
	var errors []string
	isSchoolSupervision := isSchoolSupervisionWorkbook(rows[headerRowIndex+1:], dateCol)

	for i := headerRowIndex + 1; i < len(rows); i++ {
		row := rows[i]
		get := func(col int) string {
			if col >= 0 && col < len(row) {
				return strings.TrimSpace(row[col])
			}
			return ""
		}
		date := get(dateCol)
		name := get(nameCol)
		studentID := get(studentIDCol)
		content := get(contentCol)
		scoreStr := get(scoreCol)

		if studentID == "" && content == "" {
			continue
		}
		if studentID == "" {
			errors = append(errors, fmt.Sprintf("第 %d 行: 学号为空", i+1))
			continue
		}

		var score float64
		if scoreStr != "" {
			fmt.Sscanf(scoreStr, "%f", &score)
		}
		if isSchoolSupervision {
			convertedDate, err := schoolSupervisionDate(date)
			if err != nil {
				errors = append(errors, fmt.Sprintf("第 %d 行: %s", i+1, err.Error()))
				continue
			}
			date = convertedDate
			if score < 0 {
				score = -score
			}
		}

		r := models.DeductionRecord{
			SubmitDate:        date,
			StudentName:       name,
			Content:           content,
			Score:             score,
			SchoolSupervision: isSchoolSupervision,
		}
		rec, err := models.CreateDeductionRecordForStudents(a.DB, r, studentID)
		if err != nil {
			errors = append(errors, fmt.Sprintf("第 %d 行: %s", i+1, err.Error()))
			continue
		}
		imported = append(imported, *rec)
	}

	result := map[string]any{"ok": true, "imported": len(imported), "records": imported}
	if len(errors) > 0 {
		result["errors"] = errors
	}
	log.Printf("[扣分] 批量导入: 成功 %d 条, 失败 %d 条", len(imported), len(errors))
	writeJSON(w, http.StatusOK, result)
}

var monthDayPattern = regexp.MustCompile(`^(\d{1,2})\.(\d{1,2})$`)

func isSchoolSupervisionWorkbook(rows [][]string, dateCol int) bool {
	if dateCol < 0 {
		return false
	}
	for _, row := range rows {
		if dateCol < len(row) && strings.TrimSpace(row[dateCol]) == "3.1" {
			return true
		}
	}
	return false
}

func schoolSupervisionDate(value string) (string, error) {
	value = strings.TrimSpace(value)
	matches := monthDayPattern.FindStringSubmatch(value)
	if matches == nil {
		return "", fmt.Errorf("校督日期 %q 格式不正确，应为“月.日”", value)
	}
	month, _ := strconv.Atoi(matches[1])
	day, _ := strconv.Atoi(matches[2])
	date := time.Date(time.Now().Year(), time.Month(month), day, 0, 0, 0, 0, time.Local)
	if date.Month() != time.Month(month) || date.Day() != day {
		return "", fmt.Errorf("校督日期 %q 不存在", value)
	}
	return date.Format("2006-01-02 15:04:05"), nil
}

func findDeductionHeader(rows [][]string) (headerRowIndex, dateCol, nameCol, studentIDCol, contentCol, scoreCol int) {
	const maxHeaderRows = 10
	for rowIndex, row := range rows {
		if rowIndex >= maxHeaderRows {
			break
		}
		dateCol, nameCol, studentIDCol, contentCol, scoreCol = -1, -1, -1, -1, -1
		for colIndex, value := range row {
			switch normalizeExcelHeader(value) {
			case "日期":
				dateCol = colIndex
			case "姓名":
				nameCol = colIndex
			case "学号", "违规学号":
				studentIDCol = colIndex
			case "扣分内容", "扣分项目":
				contentCol = colIndex
			case "分数":
				scoreCol = colIndex
			}
		}
		if studentIDCol >= 0 {
			return rowIndex, dateCol, nameCol, studentIDCol, contentCol, scoreCol
		}
	}
	return -1, -1, -1, -1, -1, -1
}

func normalizeExcelHeader(value string) string {
	value = strings.TrimPrefix(value, "\ufeff")
	value = strings.ReplaceAll(value, "\u00a0", " ")
	value = strings.ReplaceAll(value, "\u3000", " ")
	return strings.TrimSpace(value)
}

func (a *App) DownloadDeductionTemplate(w http.ResponseWriter, r *http.Request) {
	f, err := deductionTemplate.Open("embedded/deduction_template.xlsx")
	if err != nil {
		writeError(w, http.StatusInternalServerError, "模板文件不存在")
		return
	}
	defer f.Close()

	w.Header().Set("Content-Type", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet")
	w.Header().Set("Content-Disposition", `attachment; filename="deduction_template.xlsx"; filename*=UTF-8''%E8%AD%A6%E5%8A%A1%E5%8C%96%E5%8D%95%E9%A1%B9%E6%89%A3%E5%88%86%E5%AF%BC%E5%85%A5-%E6%A8%A1%E6%9D%BF.xlsx`)
	w.WriteHeader(http.StatusOK)
	io.Copy(w, f)
}
