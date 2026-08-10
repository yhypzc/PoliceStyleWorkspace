package handlers

import (
	"bytes"
	"crypto/hmac"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math/big"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"PoliceStyleWorkspace/models"
)

func (a *App) StartDailyReportScheduler() {
	go func() {
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for range ticker.C {
			config, err := models.GetDailyReportConfig(a.DB)
			if err != nil || config == nil || config.SetStatus == 0 || config.FetchTime == "" {
				continue
			}
			now := time.Now()
			today := now.Format("2006-01-02")
			runKey := dailyReportRunKey(now, config.FetchTime)
			if !dailyReportTimeInTriggerWindow(now, config.FetchTime) {
				continue
			}
			claimed, err := models.ClaimDailyReportAutoRun(a.DB, runKey)
			if err != nil {
				log.Printf("[每日播报] 自动播报防重状态写入失败: %v", err)
				continue
			}
			if !claimed {
				continue
			}
			log.Printf("[每日播报] 到达设定时间 %s，开始自动播报日期 %s", config.FetchTime, today)
			if err := a.RunDailyReport(today); err != nil {
				log.Printf("[每日播报] 自动播报失败: %v", err)
			} else {
				log.Printf("[每日播报] 自动播报完成")
			}
		}
	}()
}

func dailyReportTimeReached(now time.Time, fetchTime string) bool {
	_, reached := dailyReportScheduledTime(now, fetchTime)
	return reached
}

func dailyReportTimeInTriggerWindow(now time.Time, fetchTime string) bool {
	runAt, reached := dailyReportScheduledTime(now, fetchTime)
	if !reached {
		return false
	}
	return now.Sub(runAt) < 2*time.Minute
}

func dailyReportScheduledTime(now time.Time, fetchTime string) (time.Time, bool) {
	scheduled, err := time.ParseInLocation("15:04", strings.TrimSpace(fetchTime), now.Location())
	if err != nil {
		return time.Time{}, false
	}
	runAt := time.Date(now.Year(), now.Month(), now.Day(), scheduled.Hour(), scheduled.Minute(), 0, 0, now.Location())
	return runAt, !now.Before(runAt)
}

func dailyReportRunKey(now time.Time, fetchTime string) string {
	return now.Format("2006-01-02") + " " + strings.TrimSpace(fetchTime)
}

func (a *App) RunDailyReport(today string) error {
	return a.runDailyReport(today, true)
}

func (a *App) RunDailyReportManual(today string) error {
	return a.runDailyReport(today, false)
}

func (a *App) runDailyReport(today string, requireEnabled bool) error {
	config, err := models.GetDailyReportConfig(a.DB)
	if err != nil {
		return err
	}
	if config == nil || (requireEnabled && config.SetStatus == 0) {
		return fmt.Errorf("每日播报未启用")
	}
	robots, err := models.ListDingTalkRobots(a.DB)
	if err != nil {
		return err
	}
	status, content, responseRaw, atMobiles := a.fetchDailyReport(config, today)
	var failures []string
	sent := 0
	opTime := time.Now().Format("2006-01-02 15:04:05")
	for _, robot := range robots {
		if robot.SetStatus == 0 {
			continue
		}
		sent++
		robotStatus := status
		robotContent := content
		if robotStatus == "成功" {
			if err := postDingTalk(robot, content, atMobiles); err != nil {
				robotStatus = "钉钉发送失败"
				robotContent = err.Error()
			}
		}
		if err := models.SaveDailyReportLog(a.DB, models.DailyReportLog{
			OpTime:       opTime,
			OpStatus:     robotStatus,
			FetchContent: robotContent,
			RobotName:    robot.Name,
			ResponseRaw:  responseRaw,
		}); err != nil {
			failures = append(failures, robot.Name+": 日志写入失败: "+err.Error())
		}
		if robotStatus != "成功" {
			failures = append(failures, robot.Name+": "+robotStatus+": "+robotContent)
		}
	}
	if sent == 0 {
		return fmt.Errorf("没有启用的钉钉机器人")
	}
	
	if len(failures) > 0 {
		return fmt.Errorf("daily report failures: %s", strings.Join(failures, "; "))
	}

	// 每周五发送周汇总
	reportDate, _ := parseReportDate(today)
	if reportDate.Weekday() == time.Friday {
		weeklyContent, err := a.formatWeeklySummaryMarkdown(today)
		if err != nil {
			log.Printf("[每日播报] 周汇总生成失败: %v", err)
		} else {
			for _, robot := range robots {
				if robot.SetStatus == 0 {
					continue
				}
				if err := postDingTalk(robot, weeklyContent, nil); err != nil {
					log.Printf("[每日播报] 周汇总发送失败 (机器人 %s): %v", robot.Name, err)
				} else {
					log.Printf("[每日播报] 周汇总已发送 (机器人 %s)", robot.Name)
				}
			}
		}
	}

	return nil
}


func (a *App) fetchDailyReport(config *models.DailyReportConfig, today string) (string, string, string, []string) {
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Timeout: 30 * time.Second, Jar: jar}
	if _, err := vpnLoginSession(client, config); err != nil {
		return "VPN登录失败", err.Error(), "", nil
	}
	policeSession, err := policeLoginSession(client, config)
	if err != nil {
		return "内网登录失败", err.Error(), "", nil
	}
	policeBase, err := normalizePoliceBase(config.PoliceURL)
	if err != nil {
		return "内网地址错误", err.Error(), "", nil
	}
	date := strings.ReplaceAll(today, "-", "")
	headers := policeAuthHeaders(policeBase, policeSession.AccessToken, policeSession.ProxySession)
	records, err := fetchPoliceDeductionsForDate(client, policeBase, headers, date)
	if err != nil {
		return "扣分抓取失败", err.Error(), "", nil
	}
	waiting, err := fetchPoliceWaitRecordsForDate(client, policeBase, headers, date)
	if err != nil {
		return "未指定条目抓取失败", err.Error(), marshalDailyReportRaw(records, nil), nil
	}
	content, atMobiles, err := a.formatDailyReportMessage(today, records, waiting)
	if err != nil {
		return "播报格式化失败", err.Error(), marshalDailyReportRaw(records, waiting), nil
	}
	return "成功", content, marshalDailyReportRaw(records, waiting), atMobiles
}

func marshalDailyReportRaw(deductions, waiting []map[string]any) string {
	payload := map[string]any{
		"squad_records": deductions,
		"wait_records":  waiting,
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return ""
	}
	return string(data)
}

type vpnAuthConfig struct {
	Code               int             `json:"code"`
	Message            string          `json:"message"`
	Data               vpnAuthData     `json:"data"`
	PubKey             string          `json:"pubKey"`
	PubKeyExp          string          `json:"pubKeyExp"`
	Security           vpnSecurity     `json:"security"`
	DefaultDomain      string          `json:"defaultDomain"`
	AntiReplayRand     string          `json:"antiReplayRand"`
	AuthServerInfoList []vpnAuthServer `json:"authServerInfoList"`
}

type vpnAuthData struct {
	PubKey             string          `json:"pubKey"`
	PubKeyExp          string          `json:"pubKeyExp"`
	Security           vpnSecurity     `json:"security"`
	DefaultDomain      string          `json:"defaultDomain"`
	AntiReplayRand     string          `json:"antiReplayRand"`
	AuthServerInfoList []vpnAuthServer `json:"authServerInfoList"`
}

type vpnSecurity struct {
	CSRFToken string `json:"csrfToken"`
}

type vpnAuthServer struct {
	AuthType    string `json:"authType"`
	SubType     string `json:"subType"`
	LoginDomain string `json:"loginDomain"`
}

type vpnSession struct {
	Base      string
	CSRFToken string
}

type vpnLoginResultData struct {
	GraphCheckCodeEnable bool           `json:"graphCheckCodeEnable"`
	AntiReplayRand       string         `json:"antiReplayRand"`
	SIDTicket            string         `json:"sidTicket"`
	NextService          string         `json:"nextService"`
	Ticket               string         `json:"ticket"`
	Env                  map[string]any `json:"env"`
	MockExpects          map[string]any `json:"mockExpects"`
	DefaultRedirectURL   string         `json:"defaultRedirectUrl"`
	Subtype              string         `json:"subtype"`
}

type policeSession struct {
	AccessToken  string
	ProxySession string
}

type dailyReportRecord struct {
	ID          string
	Date        string
	Names       string
	Description string
	Points      string
}

type dailyReportSemesterInfo struct {
	SemesterName  string
	WeekIndex     int
	WeekStart     string
	WeekEnd       string
	DutyDorm      string
	DutyDormPhone string
}

type probeResult struct {
	Name        string
	StatusCode  int
	ContentType string
	Summary     string
	Cookies     []string
}

func vpnLogin(client *http.Client, config *models.DailyReportConfig) error {
	_, err := vpnLoginSession(client, config)
	return err
}

func vpnLoginSession(client *http.Client, config *models.DailyReportConfig) (vpnSession, error) {
	base, err := normalizeVPNBase(config.VPNLoginURL)
	if err != nil {
		return vpnSession{}, err
	}
	if err := vpnWarmup(client, base); err != nil {
		return vpnSession{}, err
	}
	var auth vpnAuthConfig
	if err := getVPNJSON(client, base+"/passport/v1/public/authConfig?clientType=SDPBrowserClient&platform=Windows&lang=zh-CN&needTicket=1", base, &auth); err != nil {
		return vpnSession{}, err
	}
	if auth.Code != 0 {
		return vpnSession{}, fmt.Errorf("VPN认证配置获取失败: %s", auth.Message)
	}
	authData := auth.Data
	if authData.PubKey == "" {
		authData = vpnAuthData{
			PubKey:         auth.PubKey,
			PubKeyExp:      auth.PubKeyExp,
			Security:       auth.Security,
			DefaultDomain:  auth.DefaultDomain,
			AntiReplayRand: auth.AntiReplayRand,
		}
	}
	if len(authData.AuthServerInfoList) == 0 {
		authData.AuthServerInfoList = auth.AuthServerInfoList
	}
	if authData.PubKey == "" || authData.PubKeyExp == "" {
		return vpnSession{}, fmt.Errorf("VPN认证配置缺少公钥")
	}
	if authData.AntiReplayRand == "" {
		return vpnSession{}, fmt.Errorf("VPN认证配置缺少 antiReplayRand")
	}
	modulus, err := hex.DecodeString(authData.PubKey)
	if err != nil {
		return vpnSession{}, fmt.Errorf("VPN公钥无效: %w", err)
	}
	exponent, err := strconv.ParseInt(authData.PubKeyExp, 10, 32)
	if err != nil || exponent == 0 {
		exponent = 65537
	}
	pub := &rsa.PublicKey{N: new(big.Int).SetBytes(modulus), E: int(exponent)}
	// SFSDP.getSecurityPwd encrypts password + the server-provided antiReplayRand.
	password, err := encryptVPNPassword(pub, config.PasswordVPN+"_"+authData.AntiReplayRand)
	if err != nil {
		return vpnSession{}, err
	}
	username := config.UsernameVPN
	if !strings.Contains(username, "@") {
		username += "@" + vpnLoginDomain(authData)
	}
	form := url.Values{"username": {username}, "password": {password}, "graphCheckCode": {""}, "rememberPwd": {"0"}}
	req, err := http.NewRequest(http.MethodPost, base+"/passport/v1/auth/psw?clientType=SDPBrowserClient&platform=Windows&lang=zh-CN", strings.NewReader(form.Encode()))
	if err != nil {
		return vpnSession{}, err
	}
	setVPNHeaders(req, base)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if authData.Security.CSRFToken != "" {
		req.Header.Set("X-Csrf-Token", authData.Security.CSRFToken)
	}
	resp, err := client.Do(req)
	if err != nil {
		return vpnSession{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return vpnSession{}, fmt.Errorf("VPN HTTP %d: %s", resp.StatusCode, string(body))
	}
	var result struct {
		Code    int                `json:"code"`
		Message string             `json:"message"`
		Data    vpnLoginResultData `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return vpnSession{}, err
	}
	if result.Code != 0 {
		if result.Data.GraphCheckCodeEnable {
			return vpnSession{}, fmt.Errorf("VPN登录失败: 需要图形验证码")
		}
		return vpnSession{}, fmt.Errorf("VPN登录失败: %s", result.Message)
	}
	session := vpnSession{Base: base, CSRFToken: authData.Security.CSRFToken}
	if err := completeVPNAuthFlow(client, session, result.Data); err != nil {
		return vpnSession{}, err
	}
	return session, nil
}

func completeVPNAuthFlow(client *http.Client, session vpnSession, data vpnLoginResultData) error {
	if vpnEnvNeed(data.Env) && data.Ticket != "" {
		deviceID := randomHex(32)
		payload := map[string]any{
			"ticket":   data.Ticket,
			"deviceId": deviceID,
			"env": map[string]any{
				"endpoint": map[string]any{
					"device_id": deviceID,
					"device": map[string]any{
						"type": "browser",
					},
				},
			},
		}
		_ = postAtrustJSON(client, session, "/controller/v1/public/reportEnv", payload, nil)
	}
	if data.NextService == "auth/authCheck" {
		var next vpnLoginResultData
		if err := getAtrustJSON(client, session, "/passport/v1/auth/authCheck", nil, &next); err != nil {
			return fmt.Errorf("VPN认证检查失败: %w", err)
		}
		if next.SIDTicket != "" {
			data.SIDTicket = next.SIDTicket
		}
		if next.NextService != "" {
			data.NextService = next.NextService
		}
		if next.MockExpects != nil {
			data.MockExpects = next.MockExpects
		}
	}
	if data.NextService == "auth/accessCheck" {
		payload := map[string]any{}
		if data.MockExpects != nil {
			payload["mockExpects"] = data.MockExpects
		}
		var next vpnLoginResultData
		if err := postAtrustJSON(client, session, "/passport/v1/auth/accessCheck", payload, &next); err != nil {
			return fmt.Errorf("VPN访问检查失败: %w", err)
		}
		if next.SIDTicket != "" {
			data.SIDTicket = next.SIDTicket
		}
	}
	online, err := vpnOnline(client, session)
	if err == nil && online {
		return nil
	}
	if data.SIDTicket != "" {
		if err := postAtrustForm(client, session, "/passport/v1/public/sessionIdExchange", url.Values{"sidTicket": {data.SIDTicket}}, nil); err != nil {
			return fmt.Errorf("VPN会话交换失败: %w", err)
		}
		online, err = vpnOnline(client, session)
		if err == nil && online {
			return nil
		}
	}
	if err != nil {
		return fmt.Errorf("VPN在线状态确认失败: %w", err)
	}
	return fmt.Errorf("VPN登录后会话仍无效")
}

func vpnEnvNeed(env map[string]any) bool {
	if env == nil {
		return false
	}
	switch v := env["need"].(type) {
	case bool:
		return v
	case string:
		return v == "1" || strings.EqualFold(v, "true")
	case float64:
		return v != 0
	default:
		return false
	}
}

func vpnOnline(client *http.Client, session vpnSession) (bool, error) {
	var result struct {
		UserInfo any `json:"userInfo"`
		Status   any `json:"status"`
	}
	if err := getAtrustJSON(client, session, "/passport/v1/user/onlineInfo", nil, &result); err != nil {
		return false, err
	}
	return result.UserInfo != nil || result.Status != nil, nil
}

func vpnLoginDomain(authData vpnAuthData) string {
	for _, server := range authData.AuthServerInfoList {
		if server.AuthType == "auth/psw" && server.SubType == "ldap" && server.LoginDomain != "" {
			return strings.TrimLeft(server.LoginDomain, "@")
		}
	}
	for _, server := range authData.AuthServerInfoList {
		if server.AuthType == "auth/psw" && server.LoginDomain != "" && server.LoginDomain != "local" {
			return strings.TrimLeft(server.LoginDomain, "@")
		}
	}
	domain := strings.TrimLeft(authData.DefaultDomain, "@")
	if domain == "" {
		return "zjjcxy.cn"
	}
	return domain
}

func normalizeVPNBase(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", fmt.Errorf("VPN地址为空")
	}
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" {
		return "", fmt.Errorf("VPN地址无效: %s", raw)
	}
	parsed.User = nil
	parsed.Path = ""
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return strings.TrimRight(parsed.String(), "/"), nil
}

func vpnWarmup(client *http.Client, base string) error {
	req, err := http.NewRequest(http.MethodGet, base+"/portal/", nil)
	if err != nil {
		return err
	}
	setVPNHeaders(req, base)
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("VPN登录页访问失败: %w", err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	if resp.StatusCode < 200 || resp.StatusCode >= 400 {
		return fmt.Errorf("VPN登录页访问失败: HTTP %d", resp.StatusCode)
	}
	return nil
}

func getVPNJSON(client *http.Client, requestURL, base string, out any) error {
	req, err := http.NewRequest(http.MethodGet, requestURL, nil)
	if err != nil {
		return err
	}
	setVPNHeaders(req, base)
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("VPN HTTP %d: %s", resp.StatusCode, string(b))
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func getAtrustJSON(client *http.Client, session vpnSession, path string, query url.Values, out any) error {
	requestURL, err := atrustServerURL(session.Base, path)
	if err != nil {
		return err
	}
	if len(query) > 0 {
		parsed, err := url.Parse(requestURL)
		if err != nil {
			return err
		}
		q := parsed.Query()
		for key, values := range query {
			for _, value := range values {
				q.Add(key, value)
			}
		}
		parsed.RawQuery = q.Encode()
		requestURL = parsed.String()
	}
	req, err := http.NewRequest(http.MethodGet, requestURL, nil)
	if err != nil {
		return err
	}
	setVPNHeaders(req, session.Base)
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return decodeAtrustResponse(resp, out)
}

func postAtrustJSON(client *http.Client, session vpnSession, path string, payload any, out any) error {
	data, _ := json.Marshal(payload)
	requestURL, err := atrustServerURL(session.Base, path)
	if err != nil {
		return err
	}
	req, err := http.NewRequest(http.MethodPost, requestURL, bytes.NewReader(data))
	if err != nil {
		return err
	}
	setVPNHeaders(req, session.Base)
	req.Header.Set("Content-Type", "application/json")
	if session.CSRFToken != "" {
		req.Header.Set("X-Csrf-Token", session.CSRFToken)
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return decodeAtrustResponse(resp, out)
}

func postAtrustForm(client *http.Client, session vpnSession, path string, form url.Values, out any) error {
	requestURL, err := atrustServerURL(session.Base, path)
	if err != nil {
		return err
	}
	req, err := http.NewRequest(http.MethodPost, requestURL, strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	setVPNHeaders(req, session.Base)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if session.CSRFToken != "" {
		req.Header.Set("X-Csrf-Token", session.CSRFToken)
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return decodeAtrustResponse(resp, out)
}

func decodeAtrustResponse(resp *http.Response, out any) error {
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, compactResponseSummary(body))
	}
	var envelope struct {
		Code    int             `json:"code"`
		Message string          `json:"message"`
		Data    json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return err
	}
	if envelope.Code != 0 {
		if envelope.Message == "" {
			envelope.Message = compactResponseSummary(body)
		}
		return fmt.Errorf("%s", envelope.Message)
	}
	if out == nil {
		return nil
	}
	data := envelope.Data
	if len(data) == 0 || string(data) == "null" {
		data = body
	}
	return json.Unmarshal(data, out)
}

func probeAtrustEndpoint(client *http.Client, vpn vpnSession, method, path string, payload any) probeResult {
	result := probeResult{Name: method + " " + path}
	var body io.Reader
	if payload != nil {
		data, _ := json.Marshal(payload)
		body = bytes.NewReader(data)
	}
	requestURL, err := atrustServerURL(vpn.Base, path)
	if err != nil {
		result.Summary = err.Error()
		return result
	}
	req, err := http.NewRequest(method, requestURL, body)
	if err != nil {
		result.Summary = err.Error()
		return result
	}
	setVPNHeaders(req, vpn.Base)
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if vpn.CSRFToken != "" && method != http.MethodGet {
		req.Header.Set("X-Csrf-Token", vpn.CSRFToken)
	}
	resp, err := client.Do(req)
	if err != nil {
		result.Summary = err.Error()
		return result
	}
	defer resp.Body.Close()
	result.StatusCode = resp.StatusCode
	result.ContentType = resp.Header.Get("Content-Type")
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
	result.Summary = compactResponseSummary(b)
	if client.Jar != nil {
		if u, err := url.Parse(vpn.Base); err == nil {
			for _, cookie := range client.Jar.Cookies(u) {
				result.Cookies = append(result.Cookies, cookie.Name)
			}
		}
	}
	return result
}

func atrustServerURL(base, path string) (string, error) {
	requestURL, err := url.Parse(base + path)
	if err != nil {
		return "", err
	}
	q := requestURL.Query()
	if q.Get("clientType") == "" {
		q.Set("clientType", "SDPBrowserClient")
	}
	if q.Get("platform") == "" {
		q.Set("platform", "Windows")
	}
	if q.Get("lang") == "" {
		q.Set("lang", "zh-CN")
	}
	requestURL.RawQuery = q.Encode()
	return requestURL.String(), nil
}

func compactResponseSummary(b []byte) string {
	var payload any
	if err := json.Unmarshal(b, &payload); err == nil {
		payload = sanitizeResponseValue(payload)
		if out, err := json.Marshal(payload); err == nil {
			return string(out)
		}
	}
	text := strings.Join(strings.Fields(string(b)), " ")
	if len(text) > 300 {
		return text[:300] + "..."
	}
	return text
}

func sanitizeResponseValue(value any) any {
	switch v := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(v))
		for key, item := range v {
			switch strings.ToLower(key) {
			case "accesstoken", "access_token", "token", "refreshtoken", "refresh_token", "sidticket", "ticket", "userid", "username", "displayname", "description", "phone", "clientip", "email":
				if item != nil && fmt.Sprint(item) != "" {
					out[key] = "[redacted]"
				} else {
					out[key] = item
				}
			default:
				out[key] = sanitizeResponseValue(item)
			}
		}
		return out
	case []any:
		out := make([]any, len(v))
		for i, item := range v {
			out[i] = sanitizeResponseValue(item)
		}
		return out
	default:
		return value
	}
}

func setVPNHeaders(req *http.Request, base string) {
	req.Header.Set("Accept", "*/*")
	req.Header.Set("Accept-Language", "zh-CN,zh;q=0.9")
	req.Header.Set("Origin", base)
	req.Header.Set("Referer", base+"/portal/")
	req.Header.Set("Sec-Ch-Ua", `"Not)A;Brand";v="8", "Chromium";v="138"`)
	req.Header.Set("Sec-Ch-Ua-Mobile", "?0")
	req.Header.Set("Sec-Ch-Ua-Platform", `"Windows"`)
	req.Header.Set("Sec-Fetch-Dest", "empty")
	req.Header.Set("Sec-Fetch-Mode", "cors")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/138.0.0.0 Safari/537.36")
	req.Header.Set("X-Sdp-Traceid", randomTraceID())
}

func randomHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("%x", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}

func randomTraceID() string {
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("%08x", time.Now().UnixNano())
	}
	return hex.EncodeToString(b[:])
}

func encryptVPNPassword(pub *rsa.PublicKey, plaintext string) (string, error) {
	maxPlaintext := pub.Size() - 11
	if maxPlaintext <= 0 {
		return "", fmt.Errorf("VPN公钥长度无效")
	}
	plain := []byte(plaintext)
	var encrypted strings.Builder
	for len(plain) > 0 {
		n := len(plain)
		if n > maxPlaintext {
			n = maxPlaintext
		}
		chunk, err := rsa.EncryptPKCS1v15(rand.Reader, pub, plain[:n])
		if err != nil {
			return "", err
		}
		encrypted.WriteString(hex.EncodeToString(chunk))
		plain = plain[n:]
	}
	return encrypted.String(), nil
}

func policeLogin(client *http.Client, config *models.DailyReportConfig) (string, error) {
	session, err := policeLoginSession(client, config)
	if err != nil {
		return "", err
	}
	return session.AccessToken, nil
}

func (a *App) formatDailyReportMessage(today string, deductions, waiting []map[string]any) (string, []string, error) {
	reportDate, err := parseReportDate(today)
	if err != nil {
		return "", nil, err
	}
	records := unappealedReportRecords(deductions, waiting)
	info, err := a.dailyReportSemesterInfo(reportDate)
	if err != nil {
		return "", nil, err
	}
	var builder strings.Builder
	builder.WriteString("## 警务化扣分通知\n\n")
	builder.WriteString("**日期：")
	builder.WriteString(reportDate.Format("2006-01-02"))
	builder.WriteString("**\n\n")
	if info != nil {
		builder.WriteString("**周次：")
		builder.WriteString(info.SemesterName)
		builder.WriteString("第")
		builder.WriteString(strconv.Itoa(info.WeekIndex + 1))
		builder.WriteString("周（")
		builder.WriteString(info.WeekStart)
		builder.WriteString("~")
		builder.WriteString(info.WeekEnd)
		builder.WriteString("）**\n\n")
		if info.DutyDorm != "" {
			builder.WriteString("**本周包干区寝室：")
			builder.WriteString(info.DutyDorm)
			builder.WriteString("**\n\n")
		}
	}
	if len(records) == 0 {
		builder.WriteString("今日暂无记录！")
	} else {
		builder.WriteString("新增记录：")
		builder.WriteString(strconv.Itoa(len(records)))
		builder.WriteString("条\n\n")
		builder.WriteString("| 记录ID | 日期 | 名字 | 扣分项目 | 分数 |\n")
		builder.WriteString("|---|---|---|---|---|\n")
		for _, record := range records {
			builder.WriteString("| ")
			builder.WriteString(markdownEscapeTableCell(record.ID))
			builder.WriteString(" | ")
			builder.WriteString(markdownEscapeTableCell(record.Date))
			builder.WriteString(" | ")
			builder.WriteString(markdownEscapeTableCell(record.Names))
			builder.WriteString(" | ")
			builder.WriteString(markdownEscapeTableCell(record.Description))
			builder.WriteString(" | ")
			builder.WriteString(markdownEscapeTableCell(record.Points))
			builder.WriteString(" |\n")
		}
		builder.WriteString("\n对扣分项目有异议请及时私信纪检委员申诉")
	}
	atMobiles := []string{}
	if dailyReportShouldRemindDutyDorm(reportDate, info) {
		phone := strings.TrimSpace(info.DutyDormPhone)
		builder.WriteString("\n本周的包干区请寝室长@")
		builder.WriteString(phone)
		builder.WriteString("做好任务分工")
		atMobiles = append(atMobiles, phone)
	}
	return builder.String(), atMobiles, nil
}

func markdownEscapeTableCell(s string) string {
	s = strings.ReplaceAll(s, "|", "\\|")
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\r", "")
	return s
}

func formatDeductionScore(score float64) string {
	if score == 0 {
		return ""
	}
	s := strconv.FormatFloat(score, 'f', -1, 64)
	if dot := strings.IndexByte(s, '.'); dot >= 0 {
		if len(s)-dot-1 > 2 {
			s = strconv.FormatFloat(score, 'f', 2, 64)
			s = strings.TrimRight(s, "0")
			s = strings.TrimRight(s, ".")
		}
	}
	return s
}

func (a *App) formatWeeklySummaryMarkdown(today string) (string, error) {
	reportDate, err := parseReportDate(today)
	if err != nil {
		return "", err
	}
	if reportDate.Weekday() != time.Friday {
		return "", fmt.Errorf("非周五，不发送周汇总")
	}
	info, err := a.dailyReportSemesterInfo(reportDate)
	if err != nil {
		return "", err
	}
	if info == nil {
		return "", fmt.Errorf("当前日期不在任何学期范围内")
	}
	weekStart, err := time.ParseInLocation("2006-01-02", info.WeekStart, time.Local)
	if err != nil {
		return "", err
	}
	weekEnd, err := time.ParseInLocation("2006-01-02", info.WeekEnd, time.Local)
	if err != nil {
		return "", err
	}
	weekEnd = weekEnd.AddDate(0, 0, 1)

	students, err := models.ListStudents(a.DB)
	if err != nil {
		return "", err
	}
	dates := make([]string, 0, 7)
	for day := weekStart; day.Before(weekEnd); day = day.AddDate(0, 0, 1) {
		dates = append(dates, day.Format("2006-01-02"))
	}
	byID := make(map[string]*dailyStudentRow, len(students))
	for _, student := range students {
		byID[student.ID] = &dailyStudentRow{ID: student.ID, Name: student.Name, Scores: make(map[string]float64)}
	}
	if err := a.fillDailyScores(byID, weekStart, weekEnd); err != nil {
		return "", err
	}

	punishEntries, _ := a.computePunishmentEntries(weekStart, weekEnd, 0.3)
	punishSet := make(map[string]bool, len(punishEntries))
	for _, pe := range punishEntries {
		punishSet[pe.StudentID] = true
	}

	var builder strings.Builder
	builder.WriteString("## ")
	builder.WriteString(info.SemesterName)
	builder.WriteString(" 学期第 ")
	builder.WriteString(strconv.Itoa(info.WeekIndex + 1))
	builder.WriteString(" 周警务化扣分汇总\n\n")
	builder.WriteString("**")
	builder.WriteString(weekStart.Format("2006-01-02"))
	builder.WriteString(" ~ ")
	builder.WriteString(weekEnd.AddDate(0, 0, -1).Format("2006-01-02"))
	builder.WriteString("**\n\n")

	if len(punishEntries) > 0 {
		builder.WriteString("\n**本周预计惩戒名单：")
		for i, pe := range punishEntries {
			if i > 0 {
				builder.WriteString("、")
			}
			builder.WriteString(markdownEscapeTableCell(pe.StudentName))
		}
		builder.WriteString("**\n\n")
	}

	hasRecords := false
	for _, student := range students {
		for _, score := range byID[student.ID].Scores {
			if score != 0 {
				hasRecords = true
				break
			}
		}
		if hasRecords {
			break
		}
	}
	if !hasRecords {
		builder.WriteString("本周暂无扣分记录\n")
		return builder.String(), nil
	}

	builder.WriteString("| 姓名 | 学号 |")
	for _, date := range dates {
		builder.WriteString(" ")
		builder.WriteString(date[5:])
		builder.WriteString(" |")
	}
	builder.WriteString(" 合计 |\n")

	builder.WriteString("|------|------|")
	for range dates {
		builder.WriteString("------|")
	}
	builder.WriteString("------|\n")

	type rowWithTotal struct {
		name  string
		id    string
		total float64
		row   *dailyStudentRow
	}
	ordered := make([]rowWithTotal, 0, len(students))
	for _, student := range students {
		r := byID[student.ID]
		total := 0.0
		for _, score := range r.Scores {
			total += score
		}
		if total == 0 {
			continue
		}
		ordered = append(ordered, rowWithTotal{r.Name, r.ID, total, r})
	}
	sort.SliceStable(ordered, func(i, j int) bool { return ordered[i].total > ordered[j].total })

	for _, entry := range ordered {
		highlight := punishSet[entry.id]
		builder.WriteString("| ")
		if highlight {
			builder.WriteString("<font color=\"#800080\"><b>")
		}
		builder.WriteString(markdownEscapeTableCell(entry.name))
		if highlight {
			builder.WriteString("</b></font>")
		}
		builder.WriteString(" | ")
		if highlight {
			builder.WriteString("<font color=\"#800080\"><b>")
		}
		builder.WriteString(entry.id)
		if highlight {
			builder.WriteString("</b></font>")
		}
		builder.WriteString(" |")
		for _, date := range dates {
			formatted := formatDeductionScore(entry.row.Scores[date])
			if formatted == "" {
				builder.WriteString(" |")
			} else {
				builder.WriteString(" ")
				if highlight {
					builder.WriteString("<font color=\"#800080\"><b>")
				}
				builder.WriteString(formatted)
				if highlight {
					builder.WriteString("</b></font>")
				}
				builder.WriteString(" |")
			}
		}
		builder.WriteString(" ")
		if highlight {
			builder.WriteString("<font color=\"#800080\"><b>")
		}
		builder.WriteString(formatDeductionScore(entry.total))
		if highlight {
			builder.WriteString("</b></font>")
		}
		builder.WriteString(" |\n")
	}

	builder.WriteString("| **合计** | |")
	weekTotal := 0.0
	for _, date := range dates {
		dayTotal := 0.0
		for _, entry := range ordered {
			dayTotal += entry.row.Scores[date]
		}
		weekTotal += dayTotal
		formatted := formatDeductionScore(dayTotal)
		if formatted == "" {
			builder.WriteString(" |")
		} else {
			builder.WriteString(" **")
			builder.WriteString(formatted)
			builder.WriteString("** |")
		}
	}
	builder.WriteString(" **")
	builder.WriteString(formatDeductionScore(weekTotal))
	builder.WriteString("** |\n")

	// ── 惩戒名单详情（总计 >= 0.3 或逻辑分 >= 0.3）──
	allEntries, _ := a.computePunishmentEntries(weekStart, weekEnd, 0.0)
	allByID := make(map[string]*punishmentEntry, len(allEntries))
	for i := range allEntries {
		allByID[allEntries[i].StudentID] = &allEntries[i]
	}
	type detailStudent struct {
		name        string
		id          string
		weeklyTotal float64
		logicTotal  float64
		records     []punishmentRecord
	}
	detailed := make(map[string]*detailStudent)
	for _, entry := range ordered {
		if entry.total >= 0.3 {
			ds := &detailStudent{name: entry.name, id: entry.id, weeklyTotal: entry.total}
			if pe := allByID[entry.id]; pe != nil {
				ds.logicTotal = pe.Total
				ds.records = pe.Records
			}
			detailed[entry.id] = ds
		}
	}
	for _, pe := range allEntries {
		if pe.Total >= 0.3 {
			if ds := detailed[pe.StudentID]; ds != nil {
				if ds.logicTotal == 0 {
					ds.logicTotal = pe.Total
					ds.records = pe.Records
				}
			} else {
				detailed[pe.StudentID] = &detailStudent{
					name: pe.StudentName, id: pe.StudentID,
					logicTotal: pe.Total, records: pe.Records,
				}
			}
		}
	}
	if len(detailed) > 0 {
		detailList := make([]*detailStudent, 0, len(detailed))
		for _, ds := range detailed {
			detailList = append(detailList, ds)
		}
		sort.SliceStable(detailList, func(i, j int) bool {
			if detailList[i].logicTotal == detailList[j].logicTotal {
				return detailList[i].weeklyTotal > detailList[j].weeklyTotal
			}
			return detailList[i].logicTotal > detailList[j].logicTotal
		})
		builder.WriteString("\n\n---\n\n")
		for _, ds := range detailList {
			builder.WriteString("**姓名：")
			builder.WriteString(markdownEscapeTableCell(ds.name))
			builder.WriteString("**\n\n")
			builder.WriteString("- 计入惩戒分值：")
			builder.WriteString(formatDeductionScore(ds.logicTotal))
			builder.WriteString("\n")
			builder.WriteString("- 是否惩戒：")
			if ds.logicTotal >= 0.3 {
				builder.WriteString("是")
			} else {
				builder.WriteString("否")
			}
			builder.WriteString("\n\n")
			if len(ds.records) > 0 {
				builder.WriteString("| id | 日期 | 扣分内容 | 计入综测分值 | 计入惩戒分值 |\n")
				builder.WriteString("|---|---|---|---|---|\n")
				for _, rec := range ds.records {
					builder.WriteString("| ")
					builder.WriteString(markdownEscapeTableCell(rec.RecordID))
					builder.WriteString(" | ")
					builder.WriteString(rec.Date)
					builder.WriteString(" | ")
					builder.WriteString(markdownEscapeTableCell(rec.Content))
					builder.WriteString(" | ")
					builder.WriteString(formatDeductionScore(rec.RawScore))
					builder.WriteString(" | ")
					builder.WriteString(formatDeductionScore(rec.LogicScore))
					builder.WriteString(" |\n")
				}
			}
			builder.WriteString("\n\n")
		}
		builder.WriteString("对惩戒名单有疑问请及时私信纪检委员\n")
	}

	return builder.String(), nil
}


func dailyReportShouldRemindDutyDorm(day time.Time, info *dailyReportSemesterInfo) bool {
	if info == nil || info.DutyDorm == "" || strings.TrimSpace(info.DutyDormPhone) == "" {
		return false
	}
	weekday := day.Weekday()
	return weekday == time.Saturday || weekday == time.Sunday || weekday == time.Monday
}

func (a *App) dailyReportSemesterInfo(day time.Time) (*dailyReportSemesterInfo, error) {
	semesters, err := models.ListSemesters(a.DB)
	if err != nil {
		return nil, err
	}
	for _, semester := range semesters {
		start, end, ok := semesterRange(semester)
		if !ok || day.Before(start) || !day.Before(end) {
			continue
		}
		weekIndex := int(day.Sub(start).Hours() / (24 * 7))
		weekStart := start.AddDate(0, 0, weekIndex*7)
		weekEnd := weekStart.AddDate(0, 0, 7)
		if weekEnd.After(end) {
			weekEnd = end
		}
		info := &dailyReportSemesterInfo{
			SemesterName: semester.Name,
			WeekIndex:    weekIndex,
			WeekStart:    weekStart.Format("2006-01-02"),
			WeekEnd:      weekEnd.AddDate(0, 0, -1).Format("2006-01-02"),
		}
		dorms, err := models.ListDorms(a.DB)
		if err != nil {
			return nil, err
		}
		if len(dorms) > 0 {
			dutySeq := weekIndex%len(dorms) + 1
			for _, dorm := range dorms {
				if dorm.Seq == dutySeq {
					info.DutyDorm = dorm.Name
					info.DutyDormPhone = dorm.PhoneNumber
					break
				}
			}
		}
		return info, nil
	}
	return nil, nil
}

func unappealedReportRecords(groups ...[]map[string]any) []dailyReportRecord {
	seen := map[string]struct{}{}
	records := []dailyReportRecord{}
	for _, group := range groups {
		for _, raw := range group {
			if !isUnappealedRecord(raw) {
				continue
			}
			record := normalizeDailyReportRecord(raw)
			if record.ID == "" {
				continue
			}
			if _, ok := seen[record.ID]; ok {
				continue
			}
			seen[record.ID] = struct{}{}
			records = append(records, record)
		}
	}
	sort.SliceStable(records, func(i, j int) bool {
		if records[i].Date == records[j].Date {
			return records[i].ID < records[j].ID
		}
		return records[i].Date < records[j].Date
	})
	return records
}

func isUnappealedRecord(record map[string]any) bool {
	for _, key := range []string{"appeal_initiated_id", "appeal_initiated_time", "appeal_confirm_id", "appeal_confirm_time", "appeal_review_id", "appeal_review_time", "appeal_evidence_description"} {
		if valueString(record[key]) != "" {
			return false
		}
	}
	for _, key := range []string{"appeal_images", "appeal_videos"} {
		if !emptyRecordCollection(record[key]) {
			return false
		}
	}
	return true
}

func normalizeDailyReportRecord(record map[string]any) dailyReportRecord {
	return dailyReportRecord{
		ID:          valueString(record["item_id"]),
		Date:        firstValueString(record, "submission_time", "item_attribution", "date"),
		Names:       firstValueString(record, "violation_names", "student_name", "name", "names"),
		Description: valueString(record["item_description"]),
		Points:      formatRecordPoints(record["deduct_points"]),
	}
}

func firstValueString(record map[string]any, keys ...string) string {
	for _, key := range keys {
		if value := valueString(record[key]); value != "" {
			return value
		}
	}
	return ""
}

func valueString(value any) string {
	switch v := value.(type) {
	case nil:
		return ""
	case string:
		return strings.TrimSpace(v)
	default:
		text := strings.TrimSpace(fmt.Sprint(v))
		if text == "<nil>" {
			return ""
		}
		return text
	}
}

func emptyRecordCollection(value any) bool {
	switch v := value.(type) {
	case nil:
		return true
	case []any:
		return len(v) == 0
	case []string:
		return len(v) == 0
	case string:
		text := strings.TrimSpace(v)
		return text == "" || text == "[]" || text == "null"
	default:
		return strings.TrimSpace(fmt.Sprint(v)) == ""
	}
}

func formatRecordPoints(value any) string {
	switch v := value.(type) {
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	case float32:
		return strconv.FormatFloat(float64(v), 'f', -1, 64)
	case int:
		return strconv.Itoa(v)
	case int64:
		return strconv.FormatInt(v, 10)
	case json.Number:
		return v.String()
	default:
		return valueString(value)
	}
}

func parseReportDate(value string) (time.Time, error) {
	value = strings.TrimSpace(value)
	for _, layout := range []string{"2006-01-02", "20060102"} {
		if date, err := time.ParseInLocation(layout, value, time.Local); err == nil {
			return date, nil
		}
	}
	return time.Time{}, fmt.Errorf("日期格式无效: %s", value)
}

func fetchPoliceDeductionsForDate(client *http.Client, policeBase string, headers map[string]string, itemAttribution string) ([]map[string]any, error) {
	const pageSize = 100
	page := 1
	allRecords := []map[string]any{}
	for {
		var payload struct {
			Records      []map[string]any `json:"records"`
			TotalPages   int              `json:"total_pages"`
			TotalRecords int              `json:"total_records"`
			CurrentPage  int              `json:"current_page"`
			Success      bool             `json:"success"`
			Message      string           `json:"message"`
		}
		err := postJSONWithHeaders(
			client,
			policeBase+"/api/police-style/student/squad_records?sf_request_type=fetch",
			map[string]any{"item_attribution": itemAttribution, "page": page, "page_size": pageSize},
			headers,
			&payload,
		)
		if err != nil {
			return nil, err
		}
		if payload.Message != "" && !payload.Success && len(payload.Records) == 0 {
			return nil, errors.New(payload.Message)
		}
		allRecords = append(allRecords, payload.Records...)
		totalPages := payload.TotalPages
		if totalPages <= 0 {
			totalPages = page
		}
		if page >= totalPages {
			break
		}
		page++
	}
	return allRecords, nil
}

func fetchPoliceWaitRecordsForDate(client *http.Client, policeBase string, headers map[string]string, itemAttribution string) ([]map[string]any, error) {
	const pageSize = 100
	page := 1
	allRecords := []map[string]any{}
	for {
		var payload any
		err := postJSONWithHeaders(
			client,
			policeBase+"/api/police-style/inspector/wait_records?sf_request_type=fetch",
			map[string]any{"item_attribution": itemAttribution, "page": page, "page_size": pageSize},
			headers,
			&payload,
		)
		if err != nil {
			return nil, err
		}
		records := extractRecordList(payload)
		allRecords = append(allRecords, records...)
		totalPages := extractIntField(payload, "total_pages")
		if totalPages <= 0 || page >= totalPages {
			break
		}
		page++
	}
	if len(allRecords) == 0 {
		return allRecords, nil
	}
	filtered := make([]map[string]any, 0, len(allRecords))
	for _, record := range allRecords {
		if recordMatchesAttribution(record, itemAttribution) {
			filtered = append(filtered, record)
		}
	}
	return filtered, nil
}

func extractIntField(payload any, key string) int {
	object, ok := payload.(map[string]any)
	if !ok {
		return 0
	}
	switch value := object[key].(type) {
	case int:
		return value
	case float64:
		return int(value)
	case string:
		n, _ := strconv.Atoi(value)
		return n
	default:
		return 0
	}
}

func extractRecordList(payload any) []map[string]any {
	switch v := payload.(type) {
	case []any:
		return anySliceToRecordList(v)
	case []map[string]any:
		return v
	case map[string]any:
		for _, key := range []string{"records", "data", "items", "list", "rows"} {
			if records := extractRecordList(v[key]); len(records) > 0 {
				return records
			}
		}
	}
	return nil
}

func anySliceToRecordList(items []any) []map[string]any {
	records := make([]map[string]any, 0, len(items))
	for _, item := range items {
		if record, ok := item.(map[string]any); ok {
			records = append(records, record)
		}
	}
	return records
}

func recordMatchesAttribution(record map[string]any, itemAttribution string) bool {
	for _, key := range []string{"item_attribution", "submission_time", "created_at", "create_time", "updated_at", "date"} {
		if normalizeRecordDate(record[key]) == itemAttribution {
			return true
		}
	}
	return false
}

func normalizeRecordDate(value any) string {
	text := fmt.Sprint(value)
	var digits strings.Builder
	for _, r := range text {
		if r >= '0' && r <= '9' {
			digits.WriteRune(r)
		}
	}
	out := digits.String()
	if len(out) >= 8 {
		return out[:8]
	}
	return out
}

func policeLoginSession(client *http.Client, config *models.DailyReportConfig) (policeSession, error) {
	policeBase, err := normalizePoliceBase(config.PoliceURL)
	if err != nil {
		return policeSession{}, err
	}
	proxySession := config.PoliceProxySession
	if proxySession == "" {
		proxySession, _ = detectPoliceProxySession(client, policeBase)
	}
	if err := policeWarmup(client, policeBase, proxySession); err != nil {
		return policeSession{}, err
	}
	payload := map[string]string{"username": config.UsernamePolice, "password": config.PasswordPolice}
	data, _ := json.Marshal(payload)
	req, err := http.NewRequest(http.MethodPost, policeBase+"/api/auth/login?sf_request_type=fetch", bytes.NewReader(data))
	if err != nil {
		return policeSession{}, err
	}
	setPoliceHeaders(req, policeBase, "/login")
	setPoliceProxySessionHeader(req, proxySession)
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return policeSession{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		return policeSession{}, fmt.Errorf("HTTP %d: %s", resp.StatusCode, compactResponseSummary(b))
	}
	var login struct {
		AccessToken string `json:"access_token"`
		Token       string `json:"token"`
		Message     string `json:"message"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&login); err != nil {
		return policeSession{}, err
	}
	token := login.AccessToken
	if token == "" {
		token = login.Token
	}
	if token == "" {
		if login.Message != "" {
			return policeSession{}, fmt.Errorf("响应中没有 access_token: %s", login.Message)
		}
		return policeSession{}, fmt.Errorf("响应中没有 access_token")
	}
	return policeSession{AccessToken: token, ProxySession: proxySession}, nil
}

func policeWarmup(client *http.Client, policeBase, proxySession string) error {
	req, err := http.NewRequest(http.MethodGet, policeBase+"/login", nil)
	if err != nil {
		return err
	}
	setPoliceHeaders(req, policeBase, "/login")
	setPoliceProxySessionHeader(req, proxySession)
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	req.Header.Set("Sec-Fetch-Dest", "document")
	req.Header.Set("Sec-Fetch-Mode", "navigate")
	req.Header.Set("Sec-Fetch-Site", "none")
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("内网登录页访问失败: %w", err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	if resp.StatusCode < 200 || resp.StatusCode >= 400 {
		return fmt.Errorf("内网登录页访问失败: HTTP %d", resp.StatusCode)
	}
	return nil
}

func detectPoliceProxySession(client *http.Client, policeBase string) (string, error) {
	if session := strings.TrimSpace(os.Getenv("POLICE_PROXY_SESSION")); session != "" {
		return session, nil
	}
	req, err := http.NewRequest(http.MethodGet, policeBase+"/vpn-config", nil)
	if err != nil {
		return "", err
	}
	setPoliceHeaders(req, policeBase, "/login")
	req.Header.Set("Accept", "*/*")
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 512*1024))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("vpn-config HTTP %d: %s", resp.StatusCode, compactResponseSummary(body))
	}
	return parseVPNConfigSession(body)
}

func parseVPNConfigSession(body []byte) (string, error) {
	text := string(body)
	const marker = "var encode = '"
	idx := strings.Index(text, marker)
	if idx < 0 {
		return "", fmt.Errorf("vpn-config 未找到 encode")
	}
	encoded := text[idx+len(marker):]
	end := strings.Index(encoded, "'")
	if end < 0 {
		return "", fmt.Errorf("vpn-config encode 格式无效")
	}
	raw, err := base64.StdEncoding.DecodeString(encoded[:end])
	if err != nil {
		return "", fmt.Errorf("vpn-config base64 解码失败: %w", err)
	}
	var config struct {
		Auth struct {
			Session string `json:"session"`
		} `json:"auth"`
	}
	if err := json.Unmarshal(raw, &config); err != nil {
		return "", fmt.Errorf("vpn-config JSON 解码失败: %w", err)
	}
	if strings.TrimSpace(config.Auth.Session) == "" {
		return "", fmt.Errorf("vpn-config auth.session 为空")
	}
	return config.Auth.Session, nil
}

func normalizePoliceBase(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", fmt.Errorf("内网警务化管理服务器地址为空")
	}
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" {
		return "", fmt.Errorf("内网警务化管理服务器地址无效: %s", raw)
	}
	parsed.User = nil
	parsed.Path = ""
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return strings.TrimRight(parsed.String(), "/"), nil
}

func policeAuthHeaders(policeBase, accessToken, proxySession string) map[string]string {
	headers := map[string]string{"Authorization": "Bearer " + accessToken}
	req, _ := http.NewRequest(http.MethodGet, policeBase, nil)
	setPoliceHeaders(req, policeBase, "/login")
	setPoliceProxySessionHeader(req, proxySession)
	for k, values := range req.Header {
		if len(values) > 0 {
			headers[k] = values[0]
		}
	}
	return headers
}

func setPoliceProxySessionHeader(req *http.Request, proxySession string) {
	proxySession = strings.TrimSpace(proxySession)
	if proxySession == "" {
		return
	}
	req.Header.Set("sdp-app-session", proxySession)
}

func setPoliceHeaders(req *http.Request, base, refererPath string) {
	req.Header.Set("Accept", "*/*")
	req.Header.Set("Accept-Language", "zh-CN,zh;q=0.9")
	req.Header.Set("Origin", base)
	req.Header.Set("Referer", base+refererPath)
	req.Header.Set("Sec-Ch-Ua", `"Not)A;Brand";v="8", "Chromium";v="138"`)
	req.Header.Set("Sec-Ch-Ua-Mobile", "?0")
	req.Header.Set("Sec-Ch-Ua-Platform", `"Windows"`)
	req.Header.Set("Sec-Fetch-Dest", "empty")
	req.Header.Set("Sec-Fetch-Mode", "cors")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/138.0.0.0 Safari/537.36")
}

func postDingTalk(robot models.DingTalkRobot, content string, atMobiles []string) error {
	payload := dingTalkMarkdownPayload("警务化扣分通知", content, atMobiles)
	client := &http.Client{Timeout: 20 * time.Second}
	requestURL, err := dingTalkWebhookURL(robot)
	if err != nil {
		return err
	}
	var result struct {
		ErrCode int    `json:"errcode"`
		ErrMsg  string `json:"errmsg"`
	}
	if err := postJSON(client, requestURL, payload, &result); err != nil {
		return err
	}
	if result.ErrCode != 0 {
		if result.ErrMsg == "" {
			result.ErrMsg = "钉钉机器人返回错误"
		}
		return fmt.Errorf("钉钉机器人返回错误 %d: %s", result.ErrCode, result.ErrMsg)
	}
	return nil
}

func dingTalkMarkdownPayload(title, text string, atMobiles []string) map[string]any {
	payload := map[string]any{"msgtype": "markdown", "markdown": map[string]string{"title": title, "text": text}}
	mobiles := normalizeAtMobiles(atMobiles)
	if len(mobiles) > 0 {
		payload["at"] = map[string]any{
			"atMobiles": mobiles,
			"isAtAll":   false,
		}
	}
	return payload
}

func normalizeAtMobiles(atMobiles []string) []string {
	seen := make(map[string]struct{}, len(atMobiles))
	mobiles := make([]string, 0, len(atMobiles))
	for _, mobile := range atMobiles {
		mobile = strings.TrimSpace(mobile)
		if mobile == "" {
			continue
		}
		if _, ok := seen[mobile]; ok {
			continue
		}
		seen[mobile] = struct{}{}
		mobiles = append(mobiles, mobile)
	}
	return mobiles
}

func dingTalkWebhookURL(robot models.DingTalkRobot) (string, error) {
	requestURL := strings.TrimSpace(robot.URL)
	secret := strings.TrimSpace(robot.Password)
	if requestURL == "" || secret == "" {
		return requestURL, nil
	}
	parsed, err := url.Parse(requestURL)
	if err != nil {
		return "", err
	}
	timestamp := strconv.FormatInt(time.Now().UnixMilli(), 10)
	message := timestamp + "\n" + secret
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(message))
	sign := base64.StdEncoding.EncodeToString(mac.Sum(nil))
	query := parsed.Query()
	query.Set("timestamp", timestamp)
	query.Set("sign", sign)
	parsed.RawQuery = query.Encode()
	return parsed.String(), nil
}
func postJSON(client *http.Client, url string, payload any, out any) error {
	return postJSONWithHeaders(client, url, payload, nil, out)
}
func postJSONWithHeaders(client *http.Client, url string, payload any, headers map[string]string, out any) error {
	data, _ := json.Marshal(payload)
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(b))
	}
	if out != nil {
		return json.NewDecoder(resp.Body).Decode(out)
	}
	return nil
}
func getJSONWithHeaders(client *http.Client, url string, headers map[string]string, out any) error {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}
