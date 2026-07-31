package handlers

import (
	"embed"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"PoliceStyleWorkspace/models"
	"github.com/xuri/excelize/v2"
)

//go:embed embedded/multi_deduction_template.xlsx
var multiDeductionTemplate embed.FS

func (a *App) ListMultiDeductions(w http.ResponseWriter, r *http.Request) {
	records, err := models.ListMultiDeductionRecords(a.DB)
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "records": records})
}
func (a *App) UpdateMultiDeduction(w http.ResponseWriter, r *http.Request) {
	var req models.MultiDeductionRecord
	if !decodeJSON(w, r, &req) {
		return
	}
	if err := models.UpdateMultiDeductionRecord(a.DB, r.PathValue("id"), req); err != nil {
		writeError(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}
func (a *App) DeleteMultiDeductions(w http.ResponseWriter, r *http.Request) {
	var req struct {
		IDs []string `json:"ids"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	deleted, err := models.DeleteMultiDeductionRecords(a.DB, req.IDs)
	if err != nil {
		writeError(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "deleted": deleted})
}
func (a *App) ListMultiDeductionSubrecords(w http.ResponseWriter, r *http.Request) {
	records, err := models.ListMultiDeductionSubrecords(a.DB, r.PathValue("id"))
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "subrecords": records})
}
func (a *App) SaveMultiDeductionSubrecord(w http.ResponseWriter, r *http.Request) {
	parent := r.PathValue("id")
	var req models.MultiDeductionSubrecord
	if !decodeJSON(w, r, &req) {
		return
	}
	req.BelongsTo = parent
	rec, err := models.SaveMultiDeductionSubrecord(a.DB, req)
	if err != nil {
		writeError(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "subrecord": rec})
}
func (a *App) DeleteMultiDeductionSubrecord(w http.ResponseWriter, r *http.Request) {
	if err := models.DeleteMultiDeductionSubrecord(a.DB, r.PathValue("subID")); err != nil {
		writeError(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}
func (a *App) ImportMultiDeductions(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 50<<20)
	if err := r.ParseMultipartForm(50 << 20); err != nil {
		writeError(w, 400, "文件过大，请上传小于 50 MB 的文件")
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		writeError(w, 400, "请上传 Excel 文件")
		return
	}
	defer file.Close()
	if !strings.HasSuffix(strings.ToLower(header.Filename), ".xlsx") {
		writeError(w, 400, "仅支持 .xlsx 格式")
		return
	}
	f, err := excelize.OpenReader(file)
	if err != nil {
		writeError(w, 400, "无法读取 Excel 文件")
		return
	}
	defer f.Close()
	rows, err := f.GetRows(f.GetSheetName(0))
	if err != nil || len(rows) < 2 {
		writeError(w, 400, "Excel 文件中没有数据行")
		return
	}
	cols := map[string]int{}
	for i, v := range rows[0] {
		cols[strings.TrimSpace(v)] = i
	}
	dorm, ok := cols["寝室名称"]
	date, dateOK := cols["日期"]
	content, contentOK := cols["扣分项目"]
	scoreCol, scoreOK := cols["分数"]
	if !ok || !dateOK || !contentOK || !scoreOK {
		writeError(w, 400, "表头必须包含「日期」「寝室名称」「扣分项目」「分数」列")
		return
	}
	imported := 0
	var errors []string
	get := func(row []string, col int) string {
		if col >= 0 && col < len(row) {
			return strings.TrimSpace(row[col])
		}
		return ""
	}
	for i, row := range rows[1:] {
		name := get(row, dorm)
		if name == "" {
			continue
		}
		dateText := get(row, date)
		converted, err := convertMultiSchoolDate(dateText)
		if err != nil {
			errors = append(errors, fmt.Sprintf("第 %d 行: %s", i+2, err))
			continue
		}
		score := 0.0
		if _, err := fmt.Sscanf(get(row, scoreCol), "%f", &score); err != nil {
			errors = append(errors, fmt.Sprintf("第 %d 行: 分数格式不正确", i+2))
			continue
		}
		if score < 0 {
			score = -score
		}
		_, err = models.CreateMultiDeductionRecord(a.DB, models.MultiDeductionRecord{SubmitDate: converted, DormName: name, Content: get(row, content), Score: score})
		if err != nil {
			errors = append(errors, fmt.Sprintf("第 %d 行: %s", i+2, err))
			continue
		}
		imported++
	}
	writeJSON(w, 200, map[string]any{"ok": true, "imported": imported, "errors": errors})
}

var multiMonthDayPattern = regexp.MustCompile(`^(\d{1,2})\.(\d{1,2})$`)

func convertMultiSchoolDate(value string) (string, error) {
	m := multiMonthDayPattern.FindStringSubmatch(strings.TrimSpace(value))
	if m == nil {
		return "", fmt.Errorf("日期 %q 格式不正确，应为“月.日”", value)
	}
	month, _ := strconv.Atoi(m[1])
	day, _ := strconv.Atoi(m[2])
	d := time.Date(time.Now().Year(), time.Month(month), day, 0, 0, 0, 0, time.Local)
	if int(d.Month()) != month || d.Day() != day {
		return "", fmt.Errorf("日期 %q 不存在", value)
	}
	return d.Format("2006-01-02 15:04:05"), nil
}
func (a *App) DownloadMultiDeductionTemplate(w http.ResponseWriter, r *http.Request) {
	f, err := multiDeductionTemplate.Open("embedded/multi_deduction_template.xlsx")
	if err != nil {
		writeError(w, 500, "模板不存在")
		return
	}
	defer f.Close()
	w.Header().Set("Content-Type", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet")
	w.Header().Set("Content-Disposition", "attachment; filename=multi_deduction_template.xlsx")
	io.Copy(w, f)
}
