package handlers

import (
	"archive/zip"
	"bytes"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"

	_ "embed"
)

//go:embed embedded/squad_appeal_template.doc
var squadAppealTemplate []byte

//go:embed embedded/school_appeal_template.doc
var schoolAppealTemplate []byte

// appealRecord is per-template data stored in JSON under "templates"
type appealRecord struct {
	TextContent  string   `json:"text_content"`
	DdPhotos     []string `json:"dd_photos,omitempty"`
	AppealPhotos []string `json:"appeal_photos,omitempty"`
}

type appealImage struct {
	Name string
	Path string
	Data []byte
}

type appealBatchRequest struct {
	Single []string `json:"single"`
	Multi  []string `json:"multi"`
}

type appealZipItem struct {
	Category      string
	ClassName     string
	GroupFolder   string
	RecordFolder  string
	DocName       string
	ZipName       string
	DocData       []byte
	EvidenceFiles []appealZipFile
}

type appealZipFile struct {
	Name string
	Data []byte
}

// appealStoreFile is the JSON file format: { "grade": "...", "class": "...", "templates": {...} }
type appealStoreFile struct {
	Grade     string                  `json:"grade"`
	Class     string                  `json:"class"`
	Templates map[string]appealRecord `json:"templates"`
}

type appealStore struct {
	mu      sync.Mutex
	path    string
	grade   string
	class   string
	records map[string]appealRecord
}

func (s *appealStore) load() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.records = make(map[string]appealRecord)
	raw, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if len(raw) == 0 {
		return nil
	}
	var file appealStoreFile
	if err := json.Unmarshal(raw, &file); err != nil {
		return nil // ignore corrupt file
	}
	s.grade = file.Grade
	s.class = file.Class
	if file.Templates != nil {
		s.records = file.Templates
	}
	return nil
}

func (s *appealStore) save() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	file := appealStoreFile{Grade: s.grade, Class: s.class, Templates: s.records}
	if file.Templates == nil {
		file.Templates = make(map[string]appealRecord)
	}
	data, _ := json.MarshalIndent(file, "", "  ")
	tmpPath := s.path + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0644); err != nil {
		return err
	}
	return os.Rename(tmpPath, s.path)
}

func (s *appealStore) getRecord(key string) (appealRecord, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.records[key]
	return r, ok
}

func (s *appealStore) getRecordByRecordID(recordID string) (string, appealRecord, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	prefix := recordID + "_"
	var fallbackKey string
	var fallbackRecord appealRecord
	for key, record := range s.records {
		if strings.HasPrefix(key, prefix) {
			if appealRecordHasData(record) {
				return key, record, true
			}
			if fallbackKey == "" {
				fallbackKey = key
				fallbackRecord = record
			}
		}
	}
	if fallbackKey != "" {
		return fallbackKey, fallbackRecord, true
	}
	return "", appealRecord{}, false
}

func (s *appealStore) deleteRecord(key string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.records, key)
}

func (s *appealStore) setRecord(key string, r appealRecord) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.records == nil {
		s.records = make(map[string]appealRecord)
	}
	s.records[key] = r
}

func (s *appealStore) getGrade() string  { s.mu.Lock(); defer s.mu.Unlock(); return s.grade }
func (s *appealStore) getClass() string  { s.mu.Lock(); defer s.mu.Unlock(); return s.class }
func (s *appealStore) setGrade(g string) { s.mu.Lock(); defer s.mu.Unlock(); s.grade = g }
func (s *appealStore) setClass(c string) { s.mu.Lock(); defer s.mu.Unlock(); s.class = c }

func appealRecordHasData(record appealRecord) bool {
	return strings.TrimSpace(record.TextContent) != "" || len(record.DdPhotos) > 0 || len(record.AppealPhotos) > 0
}

func appealRecordUnrecognized(names, studentIDs string) bool {
	return strings.TrimSpace(names) == "" && strings.TrimSpace(studentIDs) == ""
}

func appealEvidenceDir(configDir, key string) string {
	return filepath.Join(configDir, "evidence_"+appealRecordIDFromKey(key))
}

func appealRecordIDFromKey(key string) string {
	key = strings.TrimSpace(key)
	if len(key) >= 32 {
		return key[:32]
	}
	return key
}

func (a *App) appealStore() *appealStore {
	return &appealStore{path: filepath.Join(a.ConfigDir, "docx_templates_preferences.json")}
}

// appealConfig is the API response type (merged grade/class + per-record fields)
type appealConfig struct {
	Grade        string   `json:"grade"`
	Class        string   `json:"class"`
	TextContent  string   `json:"text_content"`
	DdPhotos     []string `json:"dd_photos,omitempty"`
	AppealPhotos []string `json:"appeal_photos,omitempty"`
}

func (a *App) GetAppealConfig(w http.ResponseWriter, r *http.Request) {
	store := a.appealStore()
	_ = store.load()
	key := r.URL.Query().Get("key")
	r2, _ := store.getRecord(key)
	cfg := appealConfig{
		Grade: store.getGrade(), Class: store.getClass(),
		TextContent: r2.TextContent, DdPhotos: r2.DdPhotos, AppealPhotos: r2.AppealPhotos,
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "config": cfg})
}

func (a *App) SaveAppealConfig(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Key         string `json:"key"`
		Grade       string `json:"grade"`
		Class       string `json:"class"`
		TextContent string `json:"text_content"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	store := a.appealStore()
	_ = store.load()
	existing, _ := store.getRecord(req.Key)
	store.setRecord(req.Key, appealRecord{
		TextContent: req.TextContent, DdPhotos: existing.DdPhotos, AppealPhotos: existing.AppealPhotos,
	})
	store.setGrade(req.Grade)
	store.setClass(req.Class)
	if err := store.save(); err != nil {
		writeError(w, http.StatusInternalServerError, "保存配置失败: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (a *App) UploadAppealPhoto(w http.ResponseWriter, r *http.Request) {
	key := r.URL.Query().Get("key")
	if key == "" {
		writeError(w, http.StatusBadRequest, "缺少记录标识")
		return
	}
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		writeError(w, http.StatusBadRequest, "文件上传解析失败")
		return
	}
	file, header, err := r.FormFile("photo")
	if err != nil {
		writeError(w, http.StatusBadRequest, "未找到上传文件")
		return
	}
	defer file.Close()

	ext := strings.ToLower(filepath.Ext(header.Filename))
	allowed := map[string]bool{".jpg": true, ".jpeg": true, ".png": true, ".webp": true}
	if !allowed[ext] {
		writeError(w, http.StatusBadRequest, "不支持的文件格式")
		return
	}

	data, err := io.ReadAll(file)
	if err != nil {
		writeError(w, http.StatusBadRequest, "读取文件失败")
		return
	}
	if err := validateAppealImage(data, ext); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	dir := appealEvidenceDir(a.ConfigDir, key)
	os.MkdirAll(dir, 0755)
	name := fmt.Sprintf("%s_%s%s", nowHexStr(), randHex(6), ext)
	os.WriteFile(filepath.Join(dir, name), data, 0644)
	// Persist photo reference immediately
	photoType := r.URL.Query().Get("type")
	store := a.appealStore()
	_ = store.load()
	r2, _ := store.getRecord(key)
	if photoType == "dd" {
		r2.DdPhotos = append(r2.DdPhotos, name)
	} else {
		r2.AppealPhotos = append(r2.AppealPhotos, name)
	}
	store.setRecord(key, r2)
	_ = store.save()
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "filename": name})
}

func (a *App) DeleteAppealPhoto(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Key      string `json:"key"`
		Filename string `json:"filename"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	// First check JSON record exists and photo is in the arrays
	store := a.appealStore()
	_ = store.load()
	r2, ok := store.getRecord(req.Key)
	found := false
	if ok {
		for _, f := range r2.DdPhotos {
			if f == req.Filename {
				found = true
				break
			}
		}
		if !found {
			for _, f := range r2.AppealPhotos {
				if f == req.Filename {
					found = true
					break
				}
			}
		}
	}
	if found {
		// Delete the photo file from evidence directory
		evidenceDir := appealEvidenceDir(a.ConfigDir, req.Key)
		os.Remove(filepath.Join(evidenceDir, req.Filename))
		// Remove from JSON arrays
		newDd := make([]string, 0, len(r2.DdPhotos))
		for _, f := range r2.DdPhotos {
			if f != req.Filename {
				newDd = append(newDd, f)
			}
		}
		newAppeal := make([]string, 0, len(r2.AppealPhotos))
		for _, f := range r2.AppealPhotos {
			if f != req.Filename {
				newAppeal = append(newAppeal, f)
			}
		}
		r2.DdPhotos = newDd
		r2.AppealPhotos = newAppeal
		// If record is now empty (no photos, no text), remove it entirely
		if len(newDd) == 0 && len(newAppeal) == 0 && r2.TextContent == "" {
			store.setRecord(req.Key, appealRecord{}) // will be omitted on next save? No - need to delete key
			store.deleteRecord(req.Key)
			// Clean up empty evidence directory
			os.Remove(evidenceDir)
		} else {
			store.setRecord(req.Key, r2)
		}
		_ = store.save()
	} else {
		// Orphaned photo file (not in JSON) - still delete it
		os.Remove(filepath.Join(appealEvidenceDir(a.ConfigDir, req.Key), req.Filename))
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (a *App) ExportAppealZip(w http.ResponseWriter, r *http.Request) {
	store := a.appealStore()
	_ = store.load()

	recordID := r.URL.Query().Get("id")
	if recordID == "" {
		writeError(w, http.StatusBadRequest, "缺少记录 ID")
		return
	}
	item, _, err := a.buildAppealZipItem(recordID, store, false)
	if err != nil {
		writeAppealBuildError(w, err)
		return
	}
	zipName := item.ZipName
	if zipName == ".zip" {
		zipName = cleanFn("appeal_" + recordID + ".zip")
	}
	var zipBuf bytes.Buffer
	zw := zip.NewWriter(&zipBuf)
	zf, _ := zw.Create(item.DocName)
	zf.Write(item.DocData)
	for _, evidence := range item.EvidenceFiles {
		fw, _ := zw.Create("证据/" + evidence.Name)
		fw.Write(evidence.Data)
	}
	zw.Close()

	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", "attachment; filename=appeal.zip; filename*=UTF-8''"+url.PathEscape(zipName))
	w.Write(zipBuf.Bytes())
}

func (a *App) BatchExportAppealZip(w http.ResponseWriter, r *http.Request) {
	var req appealBatchRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	req.Single = uniqueStrings(req.Single)
	req.Multi = uniqueStrings(req.Multi)
	if len(req.Single) == 0 && len(req.Multi) == 0 {
		writeError(w, http.StatusBadRequest, "请先勾选需要导出的扣分记录")
		return
	}

	store := a.appealStore()
	_ = store.load()
	var items []*appealZipItem
	createdAny := false
	for _, id := range append(append([]string{}, req.Single...), req.Multi...) {
		item, created, err := a.buildAppealZipItem(id, store, true)
		if err != nil {
			writeAppealBuildError(w, err)
			return
		}
		if created {
			createdAny = true
		}
		items = append(items, item)
	}
	if createdAny {
		if err := store.save(); err != nil {
			writeError(w, http.StatusInternalServerError, "保存默认申诉模板失败: "+err.Error())
			return
		}
	}

	var zipBuf bytes.Buffer
	zw := zip.NewWriter(&zipBuf)
	createdDirs := make(map[string]bool)
	for _, item := range items {
		baseParts := []string{item.Category, item.ClassName}
		if item.GroupFolder != "" {
			baseParts = append(baseParts, item.GroupFolder)
		}
		recordParts := append(baseParts, item.RecordFolder)
		addZipDirs(zw, createdDirs, recordParts...)
		recordDir := zipPath(recordParts...)
		zf, _ := zw.Create(recordDir + "/" + item.DocName)
		zf.Write(item.DocData)
		evidenceDir := recordDir + "/证据"
		addZipDirs(zw, createdDirs, append(recordParts, "证据")...)
		for _, evidence := range item.EvidenceFiles {
			fw, _ := zw.Create(evidenceDir + "/" + evidence.Name)
			fw.Write(evidence.Data)
		}
	}
	zw.Close()

	zipName := "申诉汇总.zip"
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", "attachment; filename=appeal_summary.zip; filename*=UTF-8''"+url.PathEscape(zipName))
	w.Write(zipBuf.Bytes())
}

func (a *App) buildAppealZipItem(recordID string, store *appealStore, createMissing bool) (*appealZipItem, bool, error) {
	recordDate, recordContent, recordNames, recordStudentIDs, isSchool, err := a.appealRecordInfo(recordID)
	if err != nil {
		return nil, false, err
	}
	key := recordID + "_" + recordNames + "_" + firstStudentID(recordStudentIDs)
	r2, found := store.getRecord(key)
	if !found || !appealRecordHasData(r2) {
		existingKey, existingRecord, existingFound := store.getRecordByRecordID(recordID)
		if existingFound && (!found || appealRecordHasData(existingRecord)) {
			key, r2, found = existingKey, existingRecord, true
		}
	}
	if !found {
		key, r2, found = store.getRecordByRecordID(recordID)
	}
	created := false
	if !found {
		if createMissing {
			store.setRecord(key, appealRecord{})
			found = true
			created = true
		}
	}
	grade := store.getGrade()
	class := store.getClass()
	dateMD := monthDay(recordDate)
	unrecognized := appealRecordUnrecognized(recordNames, recordStudentIDs)
	displayNames := recordNames
	if unrecognized {
		displayNames = "未认定"
	}

	var docName, zipName, recordFolder, groupFolder, category string
	if isSchool {
		if unrecognized {
			docName = cleanFn(fmt.Sprintf("未认定%s%s.doc", dateMD, recordContent))
			zipName = cleanFn(fmt.Sprintf("未认定%s%s.zip", dateMD, recordContent))
			recordFolder = cleanFn(fmt.Sprintf("未认定%s%s", dateMD, recordContent))
		} else {
			docName = cleanFn(fmt.Sprintf("%s%s%s%s.doc", recordStudentIDs, recordNames, dateMD, recordContent))
			zipName = cleanFn(fmt.Sprintf("%s%s%s%s.zip", recordStudentIDs, recordNames, dateMD, recordContent))
			recordFolder = cleanFn(fmt.Sprintf("%s%s%s%s", recordStudentIDs, recordNames, dateMD, recordContent))
		}
		category = "校督"
	} else {
		docName = cleanFn(fmt.Sprintf("%s%s.doc", dateMD, recordContent))
		if unrecognized {
			zipName = cleanFn("未认定.zip")
			groupFolder = "未认定"
		} else {
			zipName = cleanFn(fmt.Sprintf("%s%s.zip", recordNames, recordStudentIDs))
			groupFolder = appealGroupFolder(recordNames, recordStudentIDs)
		}
		recordFolder = cleanFn(fmt.Sprintf("%s%s", dateMD, recordContent))
		category = "大队督察"
	}
	if zipName == ".zip" {
		zipName = cleanFn("appeal_" + recordID + ".zip")
	}

	template := squadAppealTemplate
	if isSchool {
		template = schoolAppealTemplate
	}
	ddImages := a.loadAppealImages(key, r2.DdPhotos)
	loadedAppealImages := a.loadAppealImages(key, r2.AppealPhotos)
	appealImages := loadedAppealImages
	photoImages := ddImages
	if isSchool {
		photoImages = loadedAppealImages
		appealImages = nil
	}
	docData, err := fillAppealDoc(template, grade, class, displayNames, dateMD, dateMD, recordContent, r2.TextContent, isSchool, photoImages, appealImages)
	if err != nil {
		return nil, created, fmt.Errorf("生成申诉模板失败: %w", err)
	}
	evidenceImages := append(append([]appealImage{}, ddImages...), loadedAppealImages...)
	if isSchool {
		evidenceImages = loadedAppealImages
	}
	evidence := appealEvidenceFiles(evidenceImages)
	return &appealZipItem{
		Category:      category,
		ClassName:     appealClassFolder(class),
		GroupFolder:   groupFolder,
		RecordFolder:  recordFolder,
		DocName:       docName,
		ZipName:       zipName,
		DocData:       docData,
		EvidenceFiles: evidence,
	}, created, nil
}

func writeAppealBuildError(w http.ResponseWriter, err error) {
	if err == sql.ErrNoRows {
		writeError(w, http.StatusNotFound, "申诉记录不存在")
		return
	}
	writeError(w, http.StatusInternalServerError, err.Error())
}

func appealEvidenceFiles(images []appealImage) []appealZipFile {
	files := make([]appealZipFile, 0, len(images))
	seen := make(map[string]bool)
	for _, image := range images {
		if image.Name == "" || len(image.Data) == 0 || seen[image.Name] {
			continue
		}
		seen[image.Name] = true
		files = append(files, appealZipFile{Name: image.Name, Data: image.Data})
	}
	return files
}

func monthDay(value string) string {
	if len(value) >= 10 {
		return strings.Replace(value[5:10], "-", ".", 1)
	}
	if len(value) > 5 {
		return strings.Replace(value[5:], "-", ".", 1)
	}
	return ""
}

func firstStudentID(value string) string {
	for _, part := range strings.Split(value, ",") {
		if strings.TrimSpace(part) != "" {
			return strings.TrimSpace(part)
		}
	}
	return ""
}

func uniqueStrings(values []string) []string {
	result := make([]string, 0, len(values))
	seen := make(map[string]bool, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		result = append(result, value)
	}
	return result
}

func appealClassFolder(value string) string {
	value = cleanFn(strings.TrimSpace(value))
	if value == "" {
		return "未设置区队"
	}
	return value
}

func appealGroupFolder(names, ids string) string {
	nameParts := splitNonEmpty(names)
	idParts := splitNonEmpty(ids)
	if len(nameParts) == 0 || len(nameParts) != len(idParts) {
		fallback := cleanFn(strings.TrimSpace(names) + strings.TrimSpace(ids))
		if fallback == "" {
			return "未分组"
		}
		return fallback
	}
	type member struct {
		name string
		id   string
	}
	members := make([]member, 0, len(nameParts))
	for i := range nameParts {
		members = append(members, member{name: nameParts[i], id: idParts[i]})
	}
	sort.SliceStable(members, func(i, j int) bool {
		if members[i].id == members[j].id {
			return members[i].name < members[j].name
		}
		return members[i].id < members[j].id
	})
	sortedNames := make([]string, 0, len(members))
	sortedIDs := make([]string, 0, len(members))
	for _, item := range members {
		sortedNames = append(sortedNames, item.name)
		sortedIDs = append(sortedIDs, item.id)
	}
	return cleanFn(strings.Join(sortedNames, ",") + "(" + strings.Join(sortedIDs, ",") + ")")
}

func splitNonEmpty(value string) []string {
	parts := strings.Split(value, ",")
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		if part = strings.TrimSpace(part); part != "" {
			result = append(result, part)
		}
	}
	return result
}

func zipPath(parts ...string) string {
	nonEmpty := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.Trim(part, "/")
		if part != "" {
			nonEmpty = append(nonEmpty, part)
		}
	}
	return strings.Join(nonEmpty, "/")
}

func addZipDirs(zw *zip.Writer, created map[string]bool, parts ...string) {
	for index := 1; index <= len(parts); index++ {
		dir := zipPath(parts[:index]...)
		if dir == "" || created[dir] {
			continue
		}
		if entry, err := zw.Create(dir + "/"); err == nil {
			_, _ = entry.Write(nil)
			created[dir] = true
		}
	}
}

func (a *App) loadAppealImages(key string, names []string) []appealImage {
	if len(names) == 0 {
		return nil
	}
	images := make([]appealImage, 0, len(names))
	dir := appealEvidenceDir(a.ConfigDir, key)
	for _, name := range names {
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			continue
		}
		ext := strings.ToLower(filepath.Ext(name))
		if validateAppealImage(data, ext) != nil {
			continue
		}
		images = append(images, appealImage{
			Name: name,
			Path: filepath.Join(dir, name),
			Data: data,
		})
	}
	return images
}

// appealRecordInfo derives all filename fields from the record ID.
func (a *App) appealRecordInfo(id string) (date, content, names, studentIDs string, isSchool bool, err error) {
	var ids string
	err = a.DB.QueryRow(`
		SELECT r.submit_date, r.content,
		       COALESCE(GROUP_CONCAT(o.student_id, ','), ''),
		       COALESCE(GROUP_CONCAT(s.stu_name, ','), '')
		FROM police_style_records_single_subrecords r
		LEFT JOIN ownership_single_subrecords o ON o.record_id = r.id
		LEFT JOIN students s ON s.id = o.student_id
		WHERE r.id = ?
		GROUP BY r.id, r.submit_date, r.content`, id).Scan(&date, &content, &ids, &names)
	if err == nil {
		return date, content, names, ids, strings.HasPrefix(id, "xd_"), nil
	}
	if err != sql.ErrNoRows {
		return "", "", "", "", false, err
	}

	// Multi-deduction IDs identify a parent record; students are attached to
	// its child records, so collect each student once in database order.
	err = a.DB.QueryRow(`SELECT submit_date, content FROM police_style_records_multi_subrecords WHERE id = ?`, id).Scan(&date, &content)
	if err != nil {
		return "", "", "", "", false, err
	}
	rows, queryErr := a.DB.Query(`
		SELECT o.student_id, s.stu_name
		FROM subrecords_for_police_style_records_multi_subrecords sr
		JOIN ownership_multi_subrecords o ON o.subrecord_id = sr.id
		JOIN students s ON s.id = o.student_id
		WHERE sr.belongs_to = ?
		ORDER BY sr.id, o.student_id`, id)
	if queryErr != nil {
		return "", "", "", "", false, queryErr
	}
	defer rows.Close()
	seen := make(map[string]bool)
	var idParts, nameParts []string
	for rows.Next() {
		var studentID, studentName string
		if scanErr := rows.Scan(&studentID, &studentName); scanErr != nil {
			return "", "", "", "", false, scanErr
		}
		if seen[studentID] {
			continue
		}
		seen[studentID] = true
		idParts = append(idParts, studentID)
		nameParts = append(nameParts, studentName)
	}
	if err = rows.Err(); err != nil {
		return "", "", "", "", false, err
	}
	return date, content, strings.Join(nameParts, ","), strings.Join(idParts, ","), false, nil
}

func validateAppealImage(data []byte, ext string) error {
	if len(data) >= 12 && data[8] == 'W' && data[9] == 'E' && data[10] == 'B' && data[11] == 'P' {
		if ext != ".webp" {
			return fmt.Errorf("扩展名不匹配")
		}
		return nil
	}
	if len(data) >= 2 && data[0] == 0xFF && data[1] == 0xD8 {
		if ext != ".jpg" && ext != ".jpeg" {
			return fmt.Errorf("扩展名不匹配")
		}
		return nil
	}
	if len(data) >= 8 && data[0] == 0x89 && data[1] == 'P' && data[2] == 'N' && data[3] == 'G' {
		if ext != ".png" {
			return fmt.Errorf("扩展名不匹配")
		}
		return nil
	}
	return fmt.Errorf("无法识别的图片格式")
}

type wordAppealConfig struct {
	DocPath          string            `json:"doc_path"`
	OutputPath       string            `json:"output_path"`
	Values           map[string]string `json:"values"`
	PhotoPaths       []string          `json:"photo_paths"`
	AppealPhotoPaths []string          `json:"appeal_photo_paths"`
}

func fillAppealDoc(template []byte, grade, class, names, date, dateMD, content, textContent string, isSchool bool, photoImages, appealImages []appealImage) ([]byte, error) {
	values := map[string]string{
		"<grade>":            grade,
		"<date>":             date,
		"<class>":            class,
		"<student_ids_name>": names,
		"<record_content>":   content,
		"<text_content>":     textContent,
	}
	return renderAppealDocWithWord(template, values, appealImagePaths(photoImages), appealImagePaths(appealImages))
}

func appealImagePaths(images []appealImage) []string {
	paths := make([]string, 0, len(images))
	for _, image := range images {
		if image.Path != "" {
			paths = append(paths, image.Path)
		}
	}
	return paths
}

func renderAppealDocWithWord(template []byte, values map[string]string, photoPaths, appealPhotoPaths []string) ([]byte, error) {
	tempDir, err := os.MkdirTemp("", "appeal_doc_*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tempDir)

	docPath := filepath.Join(tempDir, "template.doc")
	outputPath := filepath.Join(tempDir, "output.doc")
	configPath := filepath.Join(tempDir, "config.json")
	scriptPath := filepath.Join(tempDir, "render.ps1")
	if err := os.WriteFile(docPath, template, 0644); err != nil {
		return nil, err
	}
	config := wordAppealConfig{
		DocPath:          docPath,
		OutputPath:       outputPath,
		Values:           values,
		PhotoPaths:       photoPaths,
		AppealPhotoPaths: appealPhotoPaths,
	}
	configData, err := json.Marshal(config)
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(configPath, configData, 0644); err != nil {
		return nil, err
	}
	if err := os.WriteFile(scriptPath, []byte(wordRenderScript), 0644); err != nil {
		return nil, err
	}

	powerShell, err := exec.LookPath("powershell.exe")
	if err != nil {
		powerShell, err = exec.LookPath("pwsh.exe")
		if err != nil {
			return nil, fmt.Errorf("未找到 PowerShell，无法调用 Word 插入图片")
		}
	}
	cmd := exec.Command(powerShell, "-NoProfile", "-ExecutionPolicy", "Bypass", "-File", scriptPath, configPath)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: 0x08000000, // CREATE_NO_WINDOW
	}
	output, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(output))
		if msg == "" {
			msg = err.Error()
		}
		return nil, fmt.Errorf("Word 插入图片失败: %s", msg)
	}
	return os.ReadFile(outputPath)
}

const wordRenderScript = `
$ErrorActionPreference = 'Stop'
$cfg = Get-Content -LiteralPath $args[0] -Raw -Encoding UTF8 | ConvertFrom-Json
$word = $null
$doc = $null
try {
  $word = New-Object -ComObject Word.Application
  $word.Visible = $false
  $word.DisplayAlerts = 0
  $doc = $word.Documents.Open($cfg.doc_path, $false, $false)

  function Replace-DocText($doc, [string]$findText, [string]$replaceText) {
    $range = $doc.Content
    while ($true) {
      $find = $range.Find
      $find.ClearFormatting()
      $find.Text = $findText
      $find.Forward = $true
      $find.Wrap = 0
      $find.Format = $false
      $find.MatchCase = $false
      $find.MatchWholeWord = $false
      $find.MatchWildcards = $false
      if (-not $find.Execute()) {
        break
      }
      $range.Text = $replaceText
      $range.SetRange($range.End, $doc.Content.End)
    }
  }

  function Insert-DocImages($doc, [string]$marker, $paths) {
    $range = $doc.Content
    $find = $range.Find
    $find.ClearFormatting()
    $find.Text = $marker
    $find.Forward = $true
    $find.Wrap = 0
    $find.Format = $false
    if (-not $find.Execute()) {
      return
    }
    $insert = $range.Duplicate
    $insert.Text = ''
    foreach ($path in @($paths)) {
      if ([string]::IsNullOrWhiteSpace($path)) {
        continue
      }
      if (-not [System.IO.File]::Exists($path)) {
        continue
      }
      $shape = $doc.InlineShapes.AddPicture($path, $false, $true, $insert)
      if ($shape.Width -gt 460) {
        $ratio = 460 / $shape.Width
        $shape.Width = 460
        $shape.Height = $shape.Height * $ratio
      }
      $insert.SetRange($shape.Range.End, $shape.Range.End)
      $insert.InsertParagraphAfter()
      $insert.SetRange($insert.End, $insert.End)
    }
  }

  foreach ($prop in $cfg.values.PSObject.Properties) {
    Replace-DocText $doc $prop.Name ([string]$prop.Value)
  }
  Insert-DocImages $doc '<photo>' $cfg.photo_paths
  Insert-DocImages $doc '<photo_appeal>' $cfg.appeal_photo_paths

  $doc.SaveAs2($cfg.output_path, 0)
  $doc.Close($false)
  $doc = $null
  $word.Quit()
  $word = $null
} finally {
  if ($doc -ne $null) {
    try { $doc.Close($false) } catch {}
  }
  if ($word -ne $null) {
    try { $word.Quit() } catch {}
  }
}
`

func cleanFn(s string) string {
	return strings.Map(func(r rune) rune {
		if strings.ContainsRune("/\\:*?\"<>|", r) {
			return '_'
		}
		return r
	}, s)
}

func nowHexStr() string    { b := make([]byte, 8); rand.Read(b); return fmt.Sprintf("%x", b) }
func randHex(n int) string { b := make([]byte, (n+1)/2); rand.Read(b); return fmt.Sprintf("%x", b)[:n] }
