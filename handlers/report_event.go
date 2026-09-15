package handlers

import (
	"log"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"PoliceStyleWorkspace/models"
)

// reportEventTestLimiter keeps repeated manual sends of one event from flooding
// the DingTalk group; the cooldown is per event so testing several rows in a row
// still works. Automatic dispatch is serialized by the scheduler goroutine.
var reportEventTestLimiter = newKeyedMinIntervalLimiter(3 * time.Second)

type keyedMinIntervalLimiter struct {
	mu       sync.Mutex
	interval time.Duration
	last     map[string]time.Time
}

func newKeyedMinIntervalLimiter(interval time.Duration) *keyedMinIntervalLimiter {
	return &keyedMinIntervalLimiter{interval: interval, last: map[string]time.Time{}}
}

func (l *keyedMinIntervalLimiter) allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	if last, ok := l.last[key]; ok && now.Sub(last) < l.interval {
		return false
	}
	l.last[key] = now
	return true
}

type reportEventRequest struct {
	ScheduledTime string   `json:"scheduled_time"`
	Content       string   `json:"content"`
	Robots        []string `json:"robots"`
}

func (r reportEventRequest) toModel() models.ReportEvent {
	return models.ReportEvent{
		ScheduledTime: r.ScheduledTime,
		Content:       r.Content,
		Robots:        r.Robots,
	}
}

func (a *App) ListReportEvents(w http.ResponseWriter, r *http.Request) {
	events, err := models.ListReportEvents(a.DB)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "events": events})
}

func (a *App) CreateReportEvent(w http.ResponseWriter, r *http.Request) {
	var req reportEventRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	event, err := models.CreateReportEvent(a.DB, req.toModel())
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	log.Printf("[定时通知] 新增 #%d 计划时间 %s 机器人 %s", event.ID, event.ScheduledTime, strings.Join(event.Robots, ","))
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "event": event})
}

func (a *App) UpdateReportEvent(w http.ResponseWriter, r *http.Request) {
	id, err := parseInt64Param(r, "id")
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "无效的定时通知ID")
		return
	}
	var req reportEventRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	event, err := models.UpdateReportEvent(a.DB, id, req.toModel())
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	log.Printf("[定时通知] 更新 #%d 计划时间 %s 机器人 %s", event.ID, event.ScheduledTime, strings.Join(event.Robots, ","))
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "event": event})
}

func (a *App) DeleteReportEvent(w http.ResponseWriter, r *http.Request) {
	id, err := parseInt64Param(r, "id")
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "无效的定时通知ID")
		return
	}
	if err := models.DeleteReportEvent(a.DB, id); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	log.Printf("[定时通知] 删除 #%d", id)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// TestReportEvent sends one event immediately ("测试", or "重试" once it has
// failed), writes the outcome into its log and refreshes its status.
func (a *App) TestReportEvent(w http.ResponseWriter, r *http.Request) {
	id, err := parseInt64Param(r, "id")
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "无效的定时通知ID")
		return
	}
	if !reportEventTestLimiter.allow(strconv.FormatInt(id, 10)) {
		writeError(w, http.StatusTooManyRequests, "请求过于频繁")
		return
	}
	event, err := models.GetReportEvent(a.DB, id)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	status, summary := a.dispatchReportEvent(*event, "手动测试")
	if err := models.SetReportEventStatus(a.DB, id, status); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	log.Printf("[定时通知] 手动发送 #%d: %s", id, summary)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "status": status, "message": summary})
}

// dispatchReportEvent posts the event content to every configured robot,
// ignoring each robot's enable/disable switch, then appends the outcome to the
// event log. It returns the resulting status (1 success / 2 failure).
func (a *App) dispatchReportEvent(event models.ReportEvent, trigger string) (int, string) {
	robots, err := models.DingTalkRobotsByNames(a.DB, event.Robots)
	if err != nil {
		line := "读取机器人失败: " + err.Error()
		_ = models.AppendReportEventLog(a.DB, event.ID, line)
		return models.ReportEventFailed, line
	}
	if len(robots) == 0 {
		line := "未找到可发送的机器人（配置的机器人可能已被删除）"
		_ = models.AppendReportEventLog(a.DB, event.ID, line)
		return models.ReportEventFailed, line
	}

	mobiles := a.reportEventAtMobiles(event.Content)
	if len(mobiles) > 0 {
		_ = models.AppendReportEventLog(a.DB, event.ID, "识别到 @"+strings.Join(mobiles, "、"))
	}

	body := markdownLineBreaks(event.Content)
	failed := make([]string, 0)
	for _, robot := range robots {
		if err := postDingTalkMessage(robot, "定时通知", body, mobiles); err != nil {
			failed = append(failed, robot.Name+": "+err.Error())
			_ = models.AppendReportEventLog(a.DB, event.ID, trigger+" 发送到机器人 "+robot.Name+" 失败: "+err.Error())
			continue
		}
		_ = models.AppendReportEventLog(a.DB, event.ID, trigger+" 发送到机器人 "+robot.Name+" 成功")
	}
	if len(failed) > 0 {
		return models.ReportEventFailed, strings.Join(failed, "; ")
	}
	return models.ReportEventSent, "已发送到 " + strings.Join(event.Robots, "、")
}

var (
	// reportEventPhonePattern matches the "@手机号" form DingTalk renders as a
	// real mention.
	reportEventPhonePattern = regexp.MustCompile(`@(1\d{10})`)
	// reportEventTokenPattern captures whatever follows an "@" so student names
	// picked from the frontend dropdown can be resolved to mobiles too.
	reportEventTokenPattern = regexp.MustCompile(`@([^\s@]{1,24})`)
)

// markdownLineBreaks turns every non-empty line of content into its own markdown
// paragraph. DingTalk's markdown renderer drops a bare "\n" on both desktop and
// mobile (only a blank line or a hard break renders), while the stored content
// keeps plain single newlines so the management table shows two consecutive
// lines. Blank lines are dropped here and re-created by the join, so any run of
// them collapses into exactly one paragraph break.
func markdownLineBreaks(content string) string {
	lines := strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n")
	paragraphs := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimRight(line, " \t")
		if line == "" {
			continue
		}
		paragraphs = append(paragraphs, line)
	}
	return strings.Join(paragraphs, "\n\n")
}

// reportEventAtMobiles collects the DingTalk atMobiles list for a broadcast
// body: explicit "@手机号" fragments first, then "@姓名" fragments resolved
// through students.phone_number.
func (a *App) reportEventAtMobiles(content string) []string {
	mobiles := make([]string, 0, 8)
	seen := map[string]struct{}{}
	add := func(mobile string) {
		mobile = strings.TrimSpace(mobile)
		if mobile == "" {
			return
		}
		if _, ok := seen[mobile]; ok {
			return
		}
		seen[mobile] = struct{}{}
		mobiles = append(mobiles, mobile)
	}

	for _, match := range reportEventPhonePattern.FindAllStringSubmatch(content, -1) {
		add(match[1])
	}

	students, err := models.StudentsWithPhone(a.DB)
	if err != nil || len(students) == 0 {
		return mobiles
	}
	// Longest name first so "@张三丰" does not resolve as "@张三".
	sort.SliceStable(students, func(i, j int) bool {
		return len([]rune(students[i].Name)) > len([]rune(students[j].Name))
	})
	for _, match := range reportEventTokenPattern.FindAllStringSubmatch(content, -1) {
		token := match[1]
		if token == "" {
			continue
		}
		for _, student := range students {
			if student.Name != "" && strings.HasPrefix(token, student.Name) {
				add(student.PhoneNumber)
				break
			}
		}
	}
	return mobiles
}
