package models

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Broadcast (report) event status values stored in report_events.status.
const (
	ReportEventPending = 0 // 还未播报
	ReportEventSent    = 1 // 播报成功
	ReportEventFailed  = 2 // 播报失败
)

// ReportEvent is one scheduled broadcast. Content is a DingTalk markdown body
// that may contain @手机号 mentions; Logs accumulates one line per attempt.
type ReportEvent struct {
	ID            int64    `json:"id"`
	ScheduledTime string   `json:"scheduled_time"`
	Status        int      `json:"status"`
	Content       string   `json:"content"`
	Logs          string   `json:"logs"`
	Robots        []string `json:"robots"`
}

func CreateReportEventTables(db *sql.DB) error {
	_, err := db.Exec(`PRAGMA foreign_keys=ON;
	CREATE TABLE IF NOT EXISTS report_events (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		scheduled_time TEXT,
		status INT NOT NULL DEFAULT 0,
		content TEXT,
		logs TEXT
	);
	CREATE TABLE IF NOT EXISTS report_event_to_robots (
		report_id INTEGER,
		report_robot_id TEXT,
		PRIMARY KEY (report_id, report_robot_id),
		FOREIGN KEY (report_id) REFERENCES report_events(id) ON DELETE CASCADE,
		FOREIGN KEY (report_robot_id) REFERENCES dingtalk_webbook_robots(robot_name)
	)`)
	return err
}

// ReportEventTimeLayout is the canonical storage format for scheduled_time, so
// plain string comparison keeps working for due-event queries.
const ReportEventTimeLayout = "2006-01-02 15:04:05"

func normalizeReportEventTime(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", errors.New("预计播报时间不能为空")
	}
	for _, layout := range []string{ReportEventTimeLayout, "2006-01-02T15:04:05", "2006-01-02 15:04", "2006-01-02T15:04", "2006-01-02"} {
		if parsed, err := time.ParseInLocation(layout, value, time.Local); err == nil {
			return parsed.Format(ReportEventTimeLayout), nil
		}
	}
	return "", fmt.Errorf("预计播报时间 %q 格式不正确，应为 YYYY-MM-DD HH:MM:SS", value)
}

// NormalizeReportEventTime is the exported wrapper used by the HTTP handlers.
func NormalizeReportEventTime(value string) (string, error) {
	return normalizeReportEventTime(value)
}

func reportEventRobots(db *sql.DB, id int64) ([]string, error) {
	rows, err := db.Query(`SELECT report_robot_id FROM report_event_to_robots WHERE report_id = ? ORDER BY report_robot_id ASC`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	names := make([]string, 0, 4)
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		names = append(names, name)
	}
	return names, rows.Err()
}

// ListReportEvents reads every broadcast event in schedule order. Robots are
// loaded in a second pass because the database is limited to a single
// connection, so a nested query while rows are open would deadlock.
func ListReportEvents(db *sql.DB) ([]ReportEvent, error) {
	rows, err := db.Query(`SELECT id, COALESCE(scheduled_time,''), COALESCE(status,0), COALESCE(content,''), COALESCE(logs,'')
		FROM report_events ORDER BY scheduled_time ASC, id ASC`)
	if err != nil {
		return nil, fmt.Errorf("查询定时通知失败: %w", err)
	}
	events := make([]ReportEvent, 0)
	for rows.Next() {
		var item ReportEvent
		if err := rows.Scan(&item.ID, &item.ScheduledTime, &item.Status, &item.Content, &item.Logs); err != nil {
			rows.Close()
			return nil, fmt.Errorf("扫描定时通知失败: %w", err)
		}
		events = append(events, item)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	for i := range events {
		names, err := reportEventRobots(db, events[i].ID)
		if err != nil {
			return nil, err
		}
		events[i].Robots = names
	}
	return events, nil
}

func GetReportEvent(db *sql.DB, id int64) (*ReportEvent, error) {
	var item ReportEvent
	err := db.QueryRow(`SELECT id, COALESCE(scheduled_time,''), COALESCE(status,0), COALESCE(content,''), COALESCE(logs,'')
		FROM report_events WHERE id = ?`, id).
		Scan(&item.ID, &item.ScheduledTime, &item.Status, &item.Content, &item.Logs)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, errors.New("定时通知不存在")
	}
	if err != nil {
		return nil, err
	}
	names, err := reportEventRobots(db, id)
	if err != nil {
		return nil, err
	}
	item.Robots = names
	return &item, nil
}

func validateReportEvent(event ReportEvent) (ReportEvent, error) {
	scheduled, err := normalizeReportEventTime(event.ScheduledTime)
	if err != nil {
		return event, err
	}
	event.ScheduledTime = scheduled
	event.Content = strings.TrimSpace(event.Content)
	if event.Content == "" {
		return event, errors.New("播报内容不能为空")
	}
	robots := make([]string, 0, len(event.Robots))
	seen := map[string]struct{}{}
	for _, name := range event.Robots {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		robots = append(robots, name)
	}
	if len(robots) == 0 {
		return event, errors.New("请至少选择一个发送的机器人")
	}
	event.Robots = robots
	return event, nil
}

func saveReportEventRobots(tx *sql.Tx, id int64, robots []string) error {
	if _, err := tx.Exec(`DELETE FROM report_event_to_robots WHERE report_id = ?`, id); err != nil {
		return err
	}
	for _, name := range robots {
		if _, err := tx.Exec(`INSERT INTO report_event_to_robots(report_id, report_robot_id) VALUES(?,?)`, id, name); err != nil {
			if strings.Contains(err.Error(), "FOREIGN KEY") {
				return fmt.Errorf("机器人 %q 不存在", name)
			}
			return err
		}
	}
	return nil
}

// CreateReportEvent inserts a new event and returns it with its generated id.
func CreateReportEvent(db *sql.DB, event ReportEvent) (*ReportEvent, error) {
	event, err := validateReportEvent(event)
	if err != nil {
		return nil, err
	}
	event.Status = ReportEventPending
	event.Logs = ""

	tx, err := db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	result, err := tx.Exec(`INSERT INTO report_events(scheduled_time, status, content, logs) VALUES(?,?,?,?)`,
		event.ScheduledTime, ReportEventPending, event.Content, "")
	if err != nil {
		return nil, fmt.Errorf("创建定时通知失败: %w", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return nil, err
	}
	if err := saveReportEventRobots(tx, id, event.Robots); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	event.ID = id
	return &event, nil
}

// UpdateReportEvent changes the schedule, content and target robots. Editing
// resets the event to 未播报 and clears its log, mirroring "编辑后重新定时发送".
func UpdateReportEvent(db *sql.DB, id int64, event ReportEvent) (*ReportEvent, error) {
	event, err := validateReportEvent(event)
	if err != nil {
		return nil, err
	}

	tx, err := db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	result, err := tx.Exec(`UPDATE report_events SET scheduled_time = ?, status = ?, content = ?, logs = '' WHERE id = ?`,
		event.ScheduledTime, ReportEventPending, event.Content, id)
	if err != nil {
		return nil, fmt.Errorf("更新定时通知失败: %w", err)
	}
	if affected, err := result.RowsAffected(); err != nil {
		return nil, err
	} else if affected == 0 {
		return nil, errors.New("定时通知不存在")
	}
	if err := saveReportEventRobots(tx, id, event.Robots); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	event.ID = id
	event.Status = ReportEventPending
	return &event, nil
}

func DeleteReportEvent(db *sql.DB, id int64) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`DELETE FROM report_event_to_robots WHERE report_id = ?`, id); err != nil {
		return err
	}
	result, err := tx.Exec(`DELETE FROM report_events WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("删除定时通知失败: %w", err)
	}
	if affected, err := result.RowsAffected(); err != nil {
		return err
	} else if affected == 0 {
		return errors.New("定时通知不存在")
	}
	return tx.Commit()
}

// ListDueReportEvents returns every still-pending event whose scheduled time
// has already arrived.
func ListDueReportEvents(db *sql.DB, now time.Time) ([]ReportEvent, error) {
	rows, err := db.Query(`SELECT id, COALESCE(scheduled_time,''), COALESCE(status,0), COALESCE(content,''), COALESCE(logs,'')
		FROM report_events WHERE status = ? AND scheduled_time <= ? ORDER BY scheduled_time ASC, id ASC`,
		ReportEventPending, now.Format(ReportEventTimeLayout))
	if err != nil {
		return nil, fmt.Errorf("查询到期的定时通知失败: %w", err)
	}
	events := make([]ReportEvent, 0)
	for rows.Next() {
		var item ReportEvent
		if err := rows.Scan(&item.ID, &item.ScheduledTime, &item.Status, &item.Content, &item.Logs); err != nil {
			rows.Close()
			return nil, err
		}
		events = append(events, item)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	for i := range events {
		names, err := reportEventRobots(db, events[i].ID)
		if err != nil {
			return nil, err
		}
		events[i].Robots = names
	}
	return events, nil
}

// AppendReportEventLog appends one timestamped line to the event log.
func AppendReportEventLog(db *sql.DB, id int64, line string) error {
	line = strings.TrimSpace(line)
	if line == "" {
		return nil
	}
	entry := time.Now().Format(ReportEventTimeLayout) + " " + line
	_, err := db.Exec(`UPDATE report_events
		SET logs = CASE WHEN COALESCE(logs,'') = '' THEN ? ELSE logs || char(10) || ? END
		WHERE id = ?`, entry, entry, id)
	return err
}

func SetReportEventStatus(db *sql.DB, id int64, status int) error {
	_, err := db.Exec(`UPDATE report_events SET status = ? WHERE id = ?`, status, id)
	return err
}

// DingTalkRobotsByNames resolves robot names to full robot records. Disabled
// robots are returned as well: the enable/disable switch only constrains the
// weekly report, never scheduled broadcast events.
func DingTalkRobotsByNames(db *sql.DB, names []string) ([]DingTalkRobot, error) {
	wanted := make(map[string]struct{}, len(names))
	for _, name := range names {
		if name = strings.TrimSpace(name); name != "" {
			wanted[name] = struct{}{}
		}
	}
	if len(wanted) == 0 {
		return nil, nil
	}
	all, err := ListDingTalkRobots(db)
	if err != nil {
		return nil, err
	}
	out := make([]DingTalkRobot, 0, len(wanted))
	for _, robot := range all {
		if _, ok := wanted[robot.Name]; ok {
			out = append(out, robot)
		}
	}
	return out, nil
}
