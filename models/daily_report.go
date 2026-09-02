package models

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"io"
	"strings"
	"time"
)

type DailyReportConfig struct {
	AESKey             []byte `json:"-"`
	VPNLoginURL        string `json:"vpn_login_url"`
	UsernameVPN        string `json:"username_vpn"`
	PasswordVPN        string `json:"password_vpn"`
	PoliceURL          string `json:"vpn_police_style_server_url"`
	UsernamePolice     string `json:"username_police_style_server"`
	PasswordPolice     string `json:"password_police_style_server"`
	PoliceProxySession string `json:"-"`
	FetchTime          string `json:"fetch_time_everyday"`
	SetStatus          int    `json:"set_status"`
}
type DingTalkRobot struct {
	Name      string `json:"robot_name"`
	URL       string `json:"dingtalk_webbook_url"`
	Password  string `json:"dingtalk_webbook_password"`
	SetStatus int    `json:"set_status"`
	Update    bool   `json:"update,omitempty"`
}
type DailyReportLog struct {
	LogID        string `json:"log_id"`
	RobotName    string `json:"robot_name"`
	OpTime       string `json:"op_time"`
	OpStatus     string `json:"op_status"`
	FetchContent string `json:"fetch_content"`
	RawID        string `json:"raw_id"`
	ResponseRaw  string `json:"response_raw"`
}

func CreateDailyReportTables(db *sql.DB) error {
	_, err := db.Exec(`PRAGMA foreign_keys=ON; CREATE TABLE IF NOT EXISTS daily_report_config(aes_key BLOB PRIMARY KEY, vpn_login_url TEXT, username_vpn TEXT, password_vpn BLOB, vpn_police_style_server_url TEXT, username_police_style_server TEXT, password_police_style_server BLOB, fetch_time_everyday TEXT, set_status INT); CREATE TABLE IF NOT EXISTS dingtalk_webbook_robots(robot_name TEXT PRIMARY KEY, dingtalk_webbook_url BLOB, dingtalk_webbook_password BLOB, set_status INT, aes_key BLOB, FOREIGN KEY(aes_key) REFERENCES daily_report_config(aes_key)); CREATE TABLE IF NOT EXISTS daily_report_cache(id TEXT PRIMARY KEY, response_raw TEXT); CREATE TABLE IF NOT EXISTS daily_report_auto_run(run_key TEXT PRIMARY KEY, op_time TEXT)`)
	if err != nil {
		return err
	}
	_, err = db.Exec(createDailyReportLogSQL)
	return err
}

const createDailyReportLogSQL = `CREATE TABLE IF NOT EXISTS daily_report_log(
	op_time TEXT,
	op_status TEXT,
	fetch_content TEXT,
	robot_name TEXT,
	raw_id TEXT,
	PRIMARY KEY(robot_name,op_time),
	FOREIGN KEY(robot_name) REFERENCES dingtalk_webbook_robots(robot_name),
	FOREIGN KEY(raw_id) REFERENCES daily_report_cache(id)
)`

func reportKey(db *sql.DB) ([]byte, error) {
	var key []byte
	err := db.QueryRow(`SELECT aes_key FROM daily_report_config LIMIT 1`).Scan(&key)
	if err == sql.ErrNoRows {
		key = make([]byte, 32)
		if _, err = rand.Read(key); err != nil {
			return nil, err
		}
		return key, nil
	}
	return key, err
}
func encryptReport(key []byte, plain string) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err = io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}
	return gcm.Seal(nonce, nonce, []byte(plain), nil), nil
}
func decryptReport(key, data []byte) (string, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	n := gcm.NonceSize()
	if len(data) < n {
		return "", errors.New("密文无效")
	}
	out, err := gcm.Open(nil, data[:n], data[n:], nil)
	return string(out), err
}
func GetDailyReportConfig(db *sql.DB) (*DailyReportConfig, error) {
	var c DailyReportConfig
	var p, pp []byte
	err := db.QueryRow(`SELECT aes_key,vpn_login_url,username_vpn,password_vpn,vpn_police_style_server_url,username_police_style_server,password_police_style_server,fetch_time_everyday,set_status FROM daily_report_config LIMIT 1`).Scan(&c.AESKey, &c.VPNLoginURL, &c.UsernameVPN, &p, &c.PoliceURL, &c.UsernamePolice, &pp, &c.FetchTime, &c.SetStatus)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	c.PasswordVPN, _ = decryptReport(c.AESKey, p)
	c.PasswordPolice, _ = decryptReport(c.AESKey, pp)
	return &c, nil
}
func SaveDailyReportConfig(db *sql.DB, c DailyReportConfig) error {
	key, err := reportKey(db)
	if err != nil {
		return err
	}
	p, err := encryptReport(key, c.PasswordVPN)
	if err != nil {
		return err
	}
	pp, err := encryptReport(key, c.PasswordPolice)
	if err != nil {
		return err
	}

	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	result, err := tx.Exec(`
		UPDATE daily_report_config
		SET vpn_login_url = ?,
			username_vpn = ?,
			password_vpn = ?,
			vpn_police_style_server_url = ?,
			username_police_style_server = ?,
			password_police_style_server = ?,
			fetch_time_everyday = ?,
			set_status = ?
		WHERE aes_key = ?`,
		c.VPNLoginURL,
		c.UsernameVPN,
		p,
		c.PoliceURL,
		c.UsernamePolice,
		pp,
		c.FetchTime,
		c.SetStatus,
		key,
	)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		if _, err := tx.Exec(`
			INSERT INTO daily_report_config(
				aes_key,
				vpn_login_url,
				username_vpn,
				password_vpn,
				vpn_police_style_server_url,
				username_police_style_server,
				password_police_style_server,
				fetch_time_everyday,
				set_status
			) VALUES(?,?,?,?,?,?,?,?,?)`,
			key,
			c.VPNLoginURL,
			c.UsernameVPN,
			p,
			c.PoliceURL,
			c.UsernamePolice,
			pp,
			c.FetchTime,
			c.SetStatus,
		); err != nil {
			return err
		}
	}
	return tx.Commit()
}
func ListDingTalkRobots(db *sql.DB) ([]DingTalkRobot, error) {
	key, err := reportKey(db)
	if err != nil {
		return nil, err
	}
	rows, err := db.Query(`SELECT robot_name,dingtalk_webbook_url,dingtalk_webbook_password,set_status FROM dingtalk_webbook_robots`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []DingTalkRobot{}
	for rows.Next() {
		var name string
		var u, p []byte
		var s int
		if err = rows.Scan(&name, &u, &p, &s); err != nil {
			return nil, err
		}
		url, _ := decryptReport(key, u)
		pass, _ := decryptReport(key, p)
		out = append(out, DingTalkRobot{Name: name, URL: url, Password: pass, SetStatus: s})
	}
	return out, rows.Err()
}
func SaveDingTalkRobot(db *sql.DB, r DingTalkRobot) error {
	if r.Name == "" {
		return errors.New("机器人名称不能为空")
	}
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM dingtalk_webbook_robots WHERE robot_name=?`, r.Name).Scan(&count); err != nil {
		return err
	}
	key, err := reportKey(db)
	if err != nil {
		return err
	}
	u, err := encryptReport(key, r.URL)
	if err != nil {
		return err
	}
	p, err := encryptReport(key, r.Password)
	if err != nil {
		return err
	}
	if count > 0 && !r.Update {
		return errors.New("机器人名称已存在")
	}
	if count > 0 {
		result, err := db.Exec(`UPDATE dingtalk_webbook_robots SET dingtalk_webbook_url=?, dingtalk_webbook_password=?, set_status=?, aes_key=? WHERE robot_name=?`, u, p, r.SetStatus, key, r.Name)
		if err != nil {
			return err
		}
		if affected, err := result.RowsAffected(); err != nil {
			return err
		} else if affected == 0 {
			return errors.New("机器人不存在")
		}
		return nil
	}
	_, err = db.Exec(`INSERT INTO dingtalk_webbook_robots(robot_name,dingtalk_webbook_url,dingtalk_webbook_password,set_status,aes_key) VALUES(?,?,?,?,?)`, r.Name, u, p, r.SetStatus, key)
	return err
}
func DeleteDingTalkRobot(db *sql.DB, name string) error {
	if name == "" {
		return errors.New("机器人名称不能为空")
	}
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	rows, err := tx.Query(`SELECT DISTINCT raw_id FROM daily_report_log WHERE robot_name=? AND raw_id IS NOT NULL AND raw_id <> ''`, name)
	if err != nil {
		return err
	}
	rawIDs := make([]string, 0)
	for rows.Next() {
		var rawID string
		if err := rows.Scan(&rawID); err != nil {
			rows.Close()
			return err
		}
		rawIDs = append(rawIDs, rawID)
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM daily_report_log WHERE robot_name=?`, name); err != nil {
		return err
	}
	result, err := tx.Exec(`DELETE FROM dingtalk_webbook_robots WHERE robot_name=?`, name)
	if err != nil {
		return err
	}
	if count, err := result.RowsAffected(); err != nil {
		return err
	} else if count == 0 {
		return errors.New("机器人不存在")
	}
	for _, rawID := range rawIDs {
		if _, err := tx.Exec(`DELETE FROM daily_report_cache WHERE id=? AND NOT EXISTS (SELECT 1 FROM daily_report_log WHERE raw_id=?)`, rawID, rawID); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func ClaimDailyReportAutoRun(db *sql.DB, runKey string) (bool, error) {
	if runKey == "" {
		return false, errors.New("自动播报运行键不能为空")
	}
	result, err := db.Exec(`INSERT OR IGNORE INTO daily_report_auto_run(run_key,op_time) VALUES(?,?)`, runKey, timeNowText())
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	return affected > 0, nil
}

func DailyReportRawID(responseRaw string) string {
	sum := sha256.Sum256([]byte(responseRaw))
	return hex.EncodeToString(sum[:])
}

func SaveDailyReportLog(db *sql.DB, item DailyReportLog) error {
	rawID := item.RawID
	if rawID == "" && item.ResponseRaw != "" {
		rawID = DailyReportRawID(item.ResponseRaw)
	}

	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if item.ResponseRaw != "" {
		if _, err := tx.Exec(`INSERT OR IGNORE INTO daily_report_cache(id,response_raw) VALUES(?,?)`, rawID, item.ResponseRaw); err != nil {
			return err
		}
	}
	_, err = tx.Exec(
		`INSERT OR REPLACE INTO daily_report_log(op_time,op_status,fetch_content,robot_name,raw_id) VALUES(?,?,?,?,?)`,
		item.OpTime,
		item.OpStatus,
		item.FetchContent,
		item.RobotName,
		nullableString(rawID),
	)
	if err != nil {
		return err
	}
	return tx.Commit()
}

func ListDailyReportLogs(db *sql.DB) ([]DailyReportLog, error) {
	rows, err := db.Query(`
		SELECT
			COALESCE(l.robot_name,'') || '|' || COALESCE(l.op_time,'') AS log_id,
			COALESCE(l.op_time,''),
			COALESCE(l.op_status,''),
			COALESCE(l.fetch_content,''),
			COALESCE(l.robot_name,''),
			COALESCE(l.raw_id,''),
			COALESCE(c.response_raw,'')
		FROM daily_report_log l
		LEFT JOIN daily_report_cache c ON c.id = l.raw_id
		ORDER BY l.op_time DESC, l.robot_name ASC
		LIMIT 500`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	logs := []DailyReportLog{}
	for rows.Next() {
		var item DailyReportLog
		if err := rows.Scan(&item.LogID, &item.OpTime, &item.OpStatus, &item.FetchContent, &item.RobotName, &item.RawID, &item.ResponseRaw); err != nil {
			return nil, err
		}
		logs = append(logs, item)
	}
	return logs, rows.Err()
}

func DeleteDailyReportLog(db *sql.DB, robotName, opTime string) error {
	if strings.TrimSpace(robotName) == "" || strings.TrimSpace(opTime) == "" {
		return errors.New("播报日志参数不完整")
	}

	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var rawID string
	err = tx.QueryRow(
		`SELECT COALESCE(raw_id, '') FROM daily_report_log WHERE robot_name=? AND op_time=?`,
		robotName,
		opTime,
	).Scan(&rawID)
	if errors.Is(err, sql.ErrNoRows) {
		return errors.New("播报日志不存在")
	}
	if err != nil {
		return err
	}

	result, err := tx.Exec(`DELETE FROM daily_report_log WHERE robot_name=? AND op_time=?`, robotName, opTime)
	if err != nil {
		return err
	}
	if affected, err := result.RowsAffected(); err != nil {
		return err
	} else if affected == 0 {
		return errors.New("播报日志不存在")
	}

	if rawID != "" {
		if _, err := tx.Exec(
			`DELETE FROM daily_report_cache
			 WHERE id=?
			   AND NOT EXISTS (SELECT 1 FROM daily_report_log WHERE raw_id=?)`,
			rawID,
			rawID,
		); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// ClearDailyReportLogs removes every broadcast log row and every raw
// response cache row. Because daily_report_log.raw_id references
// daily_report_cache.id, logs are deleted before the cache.
func ClearDailyReportLogs(db *sql.DB) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`DELETE FROM daily_report_log`); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM daily_report_cache`); err != nil {
		return err
	}
	return tx.Commit()
}

func DailyReportRawByLog(db *sql.DB, robotName, opTime string) (string, error) {
	var responseRaw string
	err := db.QueryRow(
		`SELECT COALESCE(c.response_raw, '')
		FROM daily_report_log l
		LEFT JOIN daily_report_cache c ON c.id = l.raw_id
		WHERE l.robot_name = ? AND l.op_time = ?`,
		robotName,
		opTime,
	).Scan(&responseRaw)
	return responseRaw, err
}

func DailyReportRawByID(db *sql.DB, rawID string) (string, error) {
	var responseRaw string
	err := db.QueryRow(`SELECT COALESCE(response_raw, '') FROM daily_report_cache WHERE id = ?`, rawID).Scan(&responseRaw)
	return responseRaw, err
}

func nullableString(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func timeNowText() string {
	return time.Now().Format("2006-01-02 15:04:05")
}
