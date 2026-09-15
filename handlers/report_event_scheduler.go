package handlers

import (
	"log"
	"time"

	"PoliceStyleWorkspace/models"
)

// reportEventTickInterval is how often the scheduler looks for due events.
const reportEventTickInterval = 15 * time.Second

// reportEventGraceWindow bounds catch-up. An event that came due while the
// service was down is not replayed once it is older than this window: it is
// marked 播报失败 with an explanatory log so the operator can press 重试.
const reportEventGraceWindow = 10 * time.Minute

// StartReportEventScheduler runs the scheduled-broadcast dispatcher. Sends are
// serialized in a single goroutine so a slow DingTalk call can never make the
// same event fire twice, and the pending→status transition afterwards keeps
// restarts idempotent.
func (a *App) StartReportEventScheduler() {
	go func() {
		ticker := time.NewTicker(reportEventTickInterval)
		defer ticker.Stop()
		for range ticker.C {
			a.dispatchDueReportEvents()
		}
	}()
}

func (a *App) dispatchDueReportEvents() {
	now := time.Now()
	events, err := models.ListDueReportEvents(a.DB, now)
	if err != nil {
		log.Printf("[定时通知] 查询到期的定时通知失败: %v", err)
		return
	}
	for _, event := range events {
		scheduled, err := models.NormalizeReportEventTime(event.ScheduledTime)
		if err != nil {
			_ = models.AppendReportEventLog(a.DB, event.ID, "预计播报时间无法解析，已标记为失败")
			_ = models.SetReportEventStatus(a.DB, event.ID, models.ReportEventFailed)
			continue
		}
		runAt, err := time.ParseInLocation(models.ReportEventTimeLayout, scheduled, time.Local)
		if err != nil {
			_ = models.AppendReportEventLog(a.DB, event.ID, "预计播报时间无法解析，已标记为失败")
			_ = models.SetReportEventStatus(a.DB, event.ID, models.ReportEventFailed)
			continue
		}
		if now.Sub(runAt) > reportEventGraceWindow {
			_ = models.AppendReportEventLog(a.DB, event.ID, "已错过预计播报时间，服务未在时间窗内运行，不做自动补播；可点击「重试」手动发送")
			_ = models.SetReportEventStatus(a.DB, event.ID, models.ReportEventFailed)
			log.Printf("[定时通知] #%d 计划时间 %s 已过期，跳过自动播报", event.ID, event.ScheduledTime)
			continue
		}

		log.Printf("[定时通知] 到达计划时间 %s，开始自动播报 #%d", event.ScheduledTime, event.ID)
		status, summary := a.dispatchReportEvent(event, "定时发送")
		if err := models.SetReportEventStatus(a.DB, event.ID, status); err != nil {
			log.Printf("[定时通知] #%d 状态写入失败: %v", event.ID, err)
		}
		if status == models.ReportEventSent {
			log.Printf("[定时通知] #%d 播报成功: %s", event.ID, summary)
		} else {
			log.Printf("[定时通知] #%d 播报失败: %s", event.ID, summary)
		}
	}
}
