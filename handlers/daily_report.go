package handlers

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"PoliceStyleWorkspace/models"
	"github.com/xuri/excelize/v2"
)

const sensitiveValueMask = "********"

var dailyReportRunPostLimiter = newMinIntervalLimiter(5 * time.Second)

type minIntervalLimiter struct {
	mu       sync.Mutex
	interval time.Duration
	last     time.Time
}

func newMinIntervalLimiter(interval time.Duration) *minIntervalLimiter {
	return &minIntervalLimiter{interval: interval}
}

func (l *minIntervalLimiter) allow() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	if !l.last.IsZero() && now.Sub(l.last) < l.interval {
		return false
	}
	l.last = now
	return true
}

func (a *App) GetDailyReportConfig(w http.ResponseWriter, r *http.Request) {
	c, err := models.GetDailyReportConfig(a.DB)
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	if c == nil {
		writeJSON(w, 200, map[string]any{"ok": true, "config": nil})
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "config": maskedDailyReportConfig(*c)})
}
func (a *App) SaveDailyReportConfig(w http.ResponseWriter, r *http.Request) {
	var c models.DailyReportConfig
	if !decodeJSON(w, r, &c) {
		return
	}
	existing, err := models.GetDailyReportConfig(a.DB)
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	preserveMaskedDailyReportConfigSecrets(&c, existing)
	if err := models.SaveDailyReportConfig(a.DB, c); err != nil {
		writeError(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}
func (a *App) ListDingTalkRobots(w http.ResponseWriter, r *http.Request) {
	v, err := models.ListDingTalkRobots(a.DB)
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	masked := make([]models.DingTalkRobot, 0, len(v))
	for _, robot := range v {
		masked = append(masked, maskedDingTalkRobot(robot))
	}
	writeJSON(w, 200, map[string]any{"ok": true, "robots": masked})
}
func (a *App) SaveDingTalkRobot(w http.ResponseWriter, r *http.Request) {
	var v models.DingTalkRobot
	if !decodeJSON(w, r, &v) {
		return
	}
	if v.Update {
		robots, err := models.ListDingTalkRobots(a.DB)
		if err != nil {
			writeError(w, 500, err.Error())
			return
		}
		for _, existing := range robots {
			if existing.Name == v.Name {
				preserveMaskedDingTalkRobotSecrets(&v, existing)
				break
			}
		}
	}
	if err := models.SaveDingTalkRobot(a.DB, v); err != nil {
		writeError(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}
func (a *App) DeleteDingTalkRobot(w http.ResponseWriter, r *http.Request) {
	var v struct {
		Name string `json:"robot_name"`
	}
	if !decodeJSON(w, r, &v) {
		return
	}
	if err := models.DeleteDingTalkRobot(a.DB, v.Name); err != nil {
		writeError(w, 400, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func maskedDailyReportConfig(config models.DailyReportConfig) models.DailyReportConfig {
	config.AESKey = nil
	config.PasswordVPN = maskSensitiveValue(config.PasswordVPN)
	config.PasswordPolice = maskSensitiveValue(config.PasswordPolice)
	return config
}

func maskedDingTalkRobot(robot models.DingTalkRobot) models.DingTalkRobot {
	robot.URL = maskSensitiveValue(robot.URL)
	robot.Password = maskSensitiveValue(robot.Password)
	return robot
}

func maskSensitiveValue(value string) string {
	if strings.TrimSpace(value) == "" {
		return ""
	}
	return sensitiveValueMask
}

func preserveMaskedDailyReportConfigSecrets(incoming, existing *models.DailyReportConfig) {
	if existing == nil {
		return
	}
	if incoming.PasswordVPN == sensitiveValueMask {
		incoming.PasswordVPN = existing.PasswordVPN
	}
	if incoming.PasswordPolice == sensitiveValueMask {
		incoming.PasswordPolice = existing.PasswordPolice
	}
}

func preserveMaskedDingTalkRobotSecrets(incoming *models.DingTalkRobot, existing models.DingTalkRobot) {
	if incoming.URL == sensitiveValueMask {
		incoming.URL = existing.URL
	}
	if incoming.Password == sensitiveValueMask {
		incoming.Password = existing.Password
	}
}

func (a *App) ListDailyReportLogs(w http.ResponseWriter, r *http.Request) {
	logs, err := models.ListDailyReportLogs(a.DB)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "logs": logs})
}

func (a *App) DeleteDailyReportLog(w http.ResponseWriter, r *http.Request) {
	var req struct {
		RobotName string `json:"robot_name"`
		OpTime    string `json:"op_time"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if err := models.DeleteDailyReportLog(a.DB, req.RobotName, req.OpTime); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (a *App) ExportDailyReportLog(w http.ResponseWriter, r *http.Request) {
	robotName := strings.TrimSpace(r.URL.Query().Get("robot_name"))
	opTime := strings.TrimSpace(r.URL.Query().Get("op_time"))
	rawID := strings.TrimSpace(r.URL.Query().Get("raw_id"))
	if rawID == "" && (robotName == "" || opTime == "") {
		writeError(w, http.StatusBadRequest, "播报日志参数不完整")
		return
	}

	var (
		responseRaw string
		err         error
	)
	if rawID != "" {
		responseRaw, err = models.DailyReportRawByID(a.DB, rawID)
	} else {
		responseRaw, err = models.DailyReportRawByLog(a.DB, robotName, opTime)
	}
	if err != nil {
		writeError(w, http.StatusNotFound, "播报日志不存在")
		return
	}

	records, err := parseDailyReportExportRecords(responseRaw)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if len(records) == 0 {
		writeError(w, http.StatusBadRequest, "该播报日志没有可导出的记录")
		return
	}

	file := excelize.NewFile()
	defer file.Close()
	sheet := file.GetSheetName(0)
	headers := []string{"日期", "姓名", "扣分项目", "分数", "违规学号"}
	for index, header := range headers {
		column, _ := excelize.ColumnNumberToName(index + 1)
		_ = file.SetCellValue(sheet, fmt.Sprintf("%s1", column), header)
	}
	for rowIndex, record := range records {
		row := rowIndex + 2
		values := []string{record.Date, record.Name, record.Description, record.Points, a.studentIDsForNames(record.Name)}
		for columnIndex, value := range values {
			column, _ := excelize.ColumnNumberToName(columnIndex + 1)
			_ = file.SetCellValue(sheet, fmt.Sprintf("%s%d", column, row), value)
		}
	}
	_ = file.SetColWidth(sheet, "A", "A", 22)
	_ = file.SetColWidth(sheet, "B", "B", 18)
	_ = file.SetColWidth(sheet, "C", "C", 36)
	_ = file.SetColWidth(sheet, "D", "D", 12)
	_ = file.SetColWidth(sheet, "E", "E", 18)

	filename := "警务化扣分记录.xlsx"
	w.Header().Set("Content-Type", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet")
	w.Header().Set("Content-Disposition", `attachment; filename*=UTF-8''`+url.QueryEscape(filename))
	_ = file.Write(w)
}

type dailyReportExportRecord struct {
	Date        string
	Name        string
	Description string
	Points      string
}

func parseDailyReportExportRecords(raw string) ([]dailyReportExportRecord, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, fmt.Errorf("播报日志没有原始抓取数据")
	}
	var payload struct {
		SquadRecords []map[string]any `json:"squad_records"`
		WaitRecords  []map[string]any `json:"wait_records"`
	}
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		return nil, fmt.Errorf("播报原始数据格式错误: %w", err)
	}

	records := make([]dailyReportExportRecord, 0, len(payload.SquadRecords)+len(payload.WaitRecords))
	for _, group := range [][]map[string]any{payload.SquadRecords, payload.WaitRecords} {
		for _, item := range group {
			records = append(records, dailyReportExportRecord{
				Date:        dailyReportExportDate(item),
				Name:        firstValueString(item, "violation_names", "student_name", "name", "names"),
				Description: valueString(item["item_description"]),
				Points:      formatRecordPoints(item["deduct_points"]),
			})
		}
	}
	return records, nil
}

// splitViolationNames removes all whitespace (including full-width and
// non-breaking spaces) from the violation-names string, normalizes full-width
// commas, then splits it on English commas into individual student names.
func splitViolationNames(value string) []string {
	value = strings.ReplaceAll(value, " ", "")
	value = strings.ReplaceAll(value, "\u3000", "")
	value = strings.ReplaceAll(value, "\u00a0", "")
	value = strings.ReplaceAll(value, "\t", "")
	value = strings.ReplaceAll(value, "，", ",")
	var names []string
	for _, part := range strings.Split(value, ",") {
		if part = strings.TrimSpace(part); part != "" {
			names = append(names, part)
		}
	}
	return names
}

// studentIDsForNames resolves each violation name to its student IDs in the
// students table and returns them joined by English commas, which matches the
// "违规学号" column consumed by the import feature. Names without a matching
// student are skipped; an empty result yields an empty cell.
func (a *App) studentIDsForNames(names string) string {
	ids := make([]string, 0, 8)
	for _, name := range splitViolationNames(names) {
		rows, err := a.DB.Query(`SELECT id FROM students WHERE stu_name = ?`, name)
		if err != nil {
			continue
		}
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err == nil {
				if id = strings.TrimSpace(id); id != "" {
					ids = append(ids, id)
				}
			}
		}
		rows.Close()
	}
	return strings.Join(ids, ",")
}

func dailyReportExportDate(record map[string]any) string {
	date := firstValueString(record, "submission_time", "item_attribution", "date")
	if len(date) == 8 && !strings.Contains(date, "-") {
		return date[:4] + "-" + date[4:6] + "-" + date[6:]
	}
	return date
}

func (a *App) RunDailyReportNow(w http.ResponseWriter, r *http.Request) {
	if !dailyReportRunPostLimiter.allow() {
		writeError(w, http.StatusTooManyRequests, "请求过于频繁")
		return
	}
	var req struct {
		Date string `json:"date"`
	}
	if r.Body != nil {
		defer r.Body.Close()
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
			writeError(w, http.StatusBadRequest, "JSON 请求体无效")
			return
		}
	}
	date := strings.TrimSpace(req.Date)
	if date == "" {
		date = time.Now().Format("2006-01-02")
	} else {
		parsed, err := parseReportDate(date)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		date = parsed.Format("2006-01-02")
	}
	if err := a.RunDailyReportManual(date); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "date": date})
}
