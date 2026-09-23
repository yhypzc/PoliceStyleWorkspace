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

// CreateDeductionRecord backs the 「添加项目」 dialog: 姓名、日期、认定学生、扣分内容、
// 分数、是否计入区队周扣分、扣分类型（大队督察扣分／校督扣分）。include_weekly is
// optional and defaults to true so older callers keep the previous behaviour.
func (a *App) CreateDeductionRecord(w http.ResponseWriter, r *http.Request) {
	var req struct {
		SubmitDate           string   `json:"submit_date"`
		StudentName          string   `json:"student_name"`
		Content              string   `json:"content"`
		Score                float64  `json:"score"`
		IncludeWeekly        *bool    `json:"include_weekly"`
		SchoolSupervision    bool     `json:"school_supervision"`
		RecognizedStudentIDs []string `json:"recognized_student_ids"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	includeWeekly := true
	if req.IncludeWeekly != nil {
		includeWeekly = *req.IncludeWeekly
	}
	rec, err := models.CreateDeductionRecordWithOwnership(a.DB, models.DeductionRecord{
		SubmitDate:        req.SubmitDate,
		StudentName:       req.StudentName,
		Content:           req.Content,
		Score:             req.Score,
		IncludeWeekly:     includeWeekly,
		SchoolSupervision: req.SchoolSupervision,
	}, req.RecognizedStudentIDs)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	kind := "大队督察"
	if req.SchoolSupervision {
		kind = "校督"
	}
	log.Printf("[扣分] 新增记录 %q (%s, 姓名: %s, 认定 %d 人, 扣分: %g)", rec.ID, kind, rec.StudentName, len(rec.RecognizedStudentIDs), rec.Score)
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
	a.CleanupAppealData(id)
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
	for _, id := range req.IDs {
		a.CleanupAppealData(id)
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

	if !strings.HasSuffix(strings.ToLower(header.Filename), ".xlsx") && !strings.HasSuffix(strings.ToLower(header.Filename), ".xls") {
		writeError(w, http.StatusBadRequest, "仅支持 .xlsx / .xls 格式")
		return
	}

	// 老式 .xls（OLE2 复合文档）excelize 读不了，按文件头判断用哪个解析器。
	// 注意 ".xlsx" 并不以 ".xls" 结尾，所以扩展名判断不会误判。
	isOLE2 := false
	magic := make([]byte, 4)
	if n, _ := io.ReadFull(file, magic); n == len(magic) {
		isOLE2 = magic[0] == 0xD0 && magic[1] == 0xCF && magic[2] == 0x11 && magic[3] == 0xE0
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		writeError(w, http.StatusBadRequest, "无法读取上传文件: "+err.Error())
		return
	}

	var imported []models.DeductionRecord
	var errs []string
	if isOLE2 {
		rows, err := readXLSRows(file)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		imported, errs = a.importDeductionRows(rows, false)
	} else {
		f, err := excelize.OpenReader(file)
		if err != nil {
			writeError(w, http.StatusBadRequest, "无法读取 Excel 文件: "+err.Error())
			return
		}
		defer f.Close()
		imported, errs = a.importDeductionWorkbook(f, false)
	}

	result := map[string]any{"ok": true, "imported": len(imported), "records": imported}
	if len(errs) > 0 {
		result["errors"] = errs
	}
	log.Printf("[扣分] 批量导入: 成功 %d 条, 失败 %d 条", len(imported), len(errs))
	writeJSON(w, http.StatusOK, result)
}

// parsedDeductionRow is a workbook row normalized to the fields the
// regular-deduction tables need, before student recognition.
type parsedDeductionRow struct {
	RowNumber         int
	SubmitDate        string
	StudentName       string
	StudentID         string
	Content           string
	Score             float64
	SchoolSupervision bool
}

// massNoticeTitlePattern matches the daily 信网学院 通报 title, e.g.
// 「信网学院日警务化管理通报结果（9月7日）」.
var massNoticeTitlePattern = regexp.MustCompile(`警务化管理通报结果\s*[（(]\s*(\d{1,2})\s*月\s*(\d{1,2})\s*日\s*[）)]`)

// importDeductionWorkbook 从已打开的 .xlsx 取出行，交给 importDeductionRows。
func (a *App) importDeductionWorkbook(f *excelize.File, skipExisting bool) (imported []models.DeductionRecord, errs []string) {
	sheetName := f.GetSheetName(0)
	if sheetName == "" {
		return nil, append(errs, "Excel 文件中没有工作表")
	}
	rows, err := f.GetRows(sheetName)
	if err != nil {
		return nil, append(errs, "读取工作表失败: "+err.Error())
	}
	return a.importDeductionRows(rows, skipExisting)
}

// importDeductionRows 是 .xlsx / .xls 共用的解析入口，按版式依次尝试：
//
//  1. 每日「警务化管理通报结果」表格（无学号列，按配置区队过滤）→ parseMassNoticeRows
//  2. 信网大队《警务化管理日常检查表》（.xls，区队分段 + 内务组/警容风纪组/生活秩序）→ parseInspectionRows
//  3. 常规导入模板（有学号列，扣分类型逐行判定，见 isSchoolSupervisionDate）
//
// 若某行违规学号为空：先用姓名字段在学生表中查学号，查到则用该学号关联；
// 姓名也查不到（或无姓名）时，作为"未认定"记录（无学生归属）入库，供后续认定。
// skipExisting 为 true 时，ID 已存在的行视为已导入并静默跳过（每日播报自动入库用）。
func (a *App) importDeductionRows(rows [][]string, skipExisting bool) (imported []models.DeductionRecord, errs []string) {
	if len(rows) < 2 {
		return nil, append(errs, "Excel 文件中没有数据行（除表头外至少需要一行数据）")
	}

	// 每日「警务化管理通报结果」表格：无学号列，按配置的区队名称过滤后导入
	if parsed, parseErrs, ok := a.parseMassNoticeRows(rows); ok {
		return a.persistDeductionRows(parsed, parseErrs, skipExisting)
	}
	// 信网大队《警务化管理日常检查表》.xls
	if parsed, parseErrs, ok := a.parseInspectionRows(rows); ok {
		return a.persistDeductionRows(parsed, parseErrs, skipExisting)
	}

	headerRowIndex, dateCol, nameCol, studentIDCol, contentCol, scoreCol := findDeductionHeader(rows)
	if headerRowIndex < 0 {
		return nil, append(errs, "表头必须包含「学号」列")
	}
	parsed := make([]parsedDeductionRow, 0, len(rows))
	// 逐行判定扣分类型：日期写「月.日」的是校督扣分，其余（含完整时间戳）是
	// 大队督察扣分。日期为空的行（表内合并单元格常见）沿用上一行的判定。
	previousSchoolSupervision := false
	for i := headerRowIndex + 1; i < len(rows); i++ {
		row := rows[i]
		get := func(col int) string {
			if col >= 0 && col < len(row) {
				return strings.TrimSpace(row[col])
			}
			return ""
		}
		name := get(nameCol)
		studentID := get(studentIDCol)
		content := get(contentCol)
		scoreStr := get(scoreCol)

		if studentID == "" && content == "" {
			continue
		}

		var score float64
		if scoreStr != "" {
			fmt.Sscanf(scoreStr, "%f", &score)
		}

		date := get(dateCol)
		isSchoolSupervision := false
		if date != "" {
			isSchoolSupervision = isSchoolSupervisionDate(date)
			previousSchoolSupervision = isSchoolSupervision
		} else {
			isSchoolSupervision = previousSchoolSupervision
			// 兼容处理：某行时间字段为空时，自动取当前时间作为该行时间再导入
			now := time.Now()
			if isSchoolSupervision {
				date = fmt.Sprintf("%d.%d", int(now.Month()), now.Day())
			} else {
				date = now.Format("2006-01-02 15:04:05")
			}
		}
		if isSchoolSupervision {
			convertedDate, err := schoolSupervisionDate(date)
			if err != nil {
				errs = append(errs, fmt.Sprintf("第 %d 行: %s", i+1, err.Error()))
				continue
			}
			date = convertedDate
			if score < 0 {
				score = -score
			}
		}

		parsed = append(parsed, parsedDeductionRow{
			RowNumber:         i + 1,
			SubmitDate:        date,
			StudentName:       name,
			StudentID:         studentID,
			Content:           content,
			Score:             score,
			SchoolSupervision: isSchoolSupervision,
		})
	}
	return a.persistDeductionRows(parsed, errs, skipExisting)
}

// persistDeductionRows inserts parsed rows. Rows carrying a 学号 use it
// directly; otherwise the 姓名 column is resolved against the students table,
// and an unresolvable row is stored as an "未认定" record for later assignment.
func (a *App) persistDeductionRows(rows []parsedDeductionRow, errs []string, skipExisting bool) ([]models.DeductionRecord, []string) {
	imported := make([]models.DeductionRecord, 0, len(rows))
	for _, row := range rows {
		r := models.DeductionRecord{
			SubmitDate:        row.SubmitDate,
			StudentName:       row.StudentName,
			Content:           row.Content,
			Score:             row.Score,
			SchoolSupervision: row.SchoolSupervision,
		}
		// 违规学号为空：先用姓名在学生表查学号；查到则改用学号关联
		studentID := row.StudentID
		if studentID == "" && row.StudentName != "" {
			studentID = a.studentIDsForNames(row.StudentName)
		}
		if studentID != "" {
			rec, err := models.CreateDeductionRecordForStudents(a.DB, r, studentID)
			if err != nil {
				if skipExisting && isDeductionDuplicateError(err) {
					continue
				}
				errs = append(errs, fmt.Sprintf("第 %d 行: %s", row.RowNumber, err.Error()))
				continue
			}
			imported = append(imported, *rec)
			continue
		}
		rec, err := models.CreateUnassignedDeductionRecord(a.DB, r)
		if err != nil {
			if skipExisting && isDeductionDuplicateError(err) {
				continue
			}
			errs = append(errs, fmt.Sprintf("第 %d 行: %s", row.RowNumber, err.Error()))
			continue
		}
		imported = append(imported, *rec)
	}
	return imported, errs
}

// parseMassNoticeRows parses the daily 信网学院「警务化管理通报结果」workbook:
// row 1 carries 「…警务化管理通报结果（M月D日）」, the header row carries
// 区队/姓名/时间/轻微违纪违规行为/建议扣分, and each following row is one
// violation. The title supplies the date (current year), the 时间 column
// (上午/下午) supplies the clock, and only rows whose 区队 equals the configured
// squadron are returned. ok=false means the workbook is not in this layout.
func (a *App) parseMassNoticeRows(rows [][]string) ([]parsedDeductionRow, []string, bool) {
	titleRow, month, day := findMassNoticeTitle(rows)
	if titleRow < 0 {
		return nil, nil, false
	}
	headerRow, squadCol, nameCol, timeCol, contentCol, scoreCol := findMassNoticeHeader(rows, titleRow)
	if headerRow < 0 {
		return nil, []string{"未找到「区队/姓名/轻微违纪违规行为/建议扣分」表头"}, true
	}
	squad, err := models.GetSquadName(a.DB)
	if err != nil {
		return nil, []string{err.Error()}, true
	}
	squad = normalizeExcelHeader(squad)
	if squad == "" {
		return nil, []string{"未配置区队名称，请先在「工作台 → 区队」中设置"}, true
	}

	now := time.Now()
	datePrefix := fmt.Sprintf("%04d-%02d-%02d", now.Year(), month, day)
	// 程序判定时间段：当前时刻在 12 点前为上午，否则为下午。
	currentPeriod := "上午"
	if now.Hour() >= 12 {
		currentPeriod = "下午"
	}
	currentClock := now.Format("15:04:05")

	parsed := make([]parsedDeductionRow, 0, len(rows))
	squadSeen := false
	currentSquad := ""
	for i := headerRow + 1; i < len(rows); i++ {
		row := rows[i]
		get := func(col int) string {
			if col >= 0 && col < len(row) {
				return normalizeExcelHeader(row[col])
			}
			return ""
		}
		// 区队只在每个区队块的首行出现，向后沿用
		if value := get(squadCol); value != "" {
			currentSquad = value
		}
		if currentSquad == squad {
			squadSeen = true
		}
		name := get(nameCol)
		content := get(contentCol)
		if name == "" || content == "" || currentSquad != squad {
			continue
		}
		// 与程序判定时间段一致时取当前时刻，否则上午 08:00:00 / 下午 18:00:00
		period := get(timeCol)
		clock := currentClock
		if period != currentPeriod {
			switch {
			case strings.Contains(period, "上午"):
				clock = "08:00:00"
			case strings.Contains(period, "下午"):
				clock = "18:00:00"
			}
		}
		var score float64
		if scoreStr := get(scoreCol); scoreStr != "" {
			fmt.Sscanf(scoreStr, "%f", &score)
		}
		parsed = append(parsed, parsedDeductionRow{
			RowNumber:   i + 1,
			SubmitDate:  datePrefix + " " + clock,
			StudentName: name,
			Content:     content,
			Score:       score,
		})
	}
	errs := make([]string, 0, 1)
	if !squadSeen {
		errs = append(errs, fmt.Sprintf("表格中未找到区队 %q 的扣分记录", squad))
	}
	return parsed, errs, true
}

// findMassNoticeTitle locates the 警务化管理通报结果 title in the first rows
// and returns its row index plus the 月/日 it carries.
func findMassNoticeTitle(rows [][]string) (rowIndex, month, day int) {
	limit := len(rows)
	if limit > 5 {
		limit = 5
	}
	for i := 0; i < limit; i++ {
		for _, cell := range rows[i] {
			matches := massNoticeTitlePattern.FindStringSubmatch(cell)
			if matches == nil {
				continue
			}
			month, _ = strconv.Atoi(matches[1])
			day, _ = strconv.Atoi(matches[2])
			if month < 1 || month > 12 || day < 1 || day > 31 {
				return -1, 0, 0
			}
			return i, month, day
		}
	}
	return -1, 0, 0
}

// findMassNoticeHeader locates the 通报 header row below the title and returns
// the column index of every field the importer consumes.
func findMassNoticeHeader(rows [][]string, titleRow int) (headerRow, squadCol, nameCol, timeCol, contentCol, scoreCol int) {
	limit := titleRow + 6
	if limit > len(rows) {
		limit = len(rows)
	}
	for i := titleRow; i < limit; i++ {
		squadCol, nameCol, timeCol, contentCol, scoreCol = -1, -1, -1, -1, -1
		for colIndex, value := range rows[i] {
			switch normalizeExcelHeader(value) {
			case "区队":
				squadCol = colIndex
			case "姓名":
				nameCol = colIndex
			case "时间":
				timeCol = colIndex
			case "轻微违纪违规行为":
				contentCol = colIndex
			case "建议扣分":
				scoreCol = colIndex
			}
		}
		if squadCol >= 0 && nameCol >= 0 && contentCol >= 0 {
			return i, squadCol, nameCol, timeCol, contentCol, scoreCol
		}
	}
	return -1, -1, -1, -1, -1, -1
}

func isDeductionDuplicateError(err error) bool {
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "unique constraint") || strings.Contains(msg, "已存在")
}

var monthDayPattern = regexp.MustCompile(`^(\d{1,2})\.(\d{1,2})$`)

// isSchoolSupervisionDate reports whether a 日期 cell is written the way 校督
// sheets write it: 「月.日」such as 9.7 / 10.12. Regular (大队督察) sheets carry a
// full `YYYY-MM-DD HH:MM:SS` timestamp, so the date shape alone tells the two
// apart row by row.
func isSchoolSupervisionDate(value string) bool {
	return monthDayPattern.MatchString(strings.TrimSpace(value))
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
