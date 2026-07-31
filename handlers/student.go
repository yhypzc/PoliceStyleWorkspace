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

	"PoliceStyleWorkspace/models"

	"github.com/xuri/excelize/v2"
)

//go:embed embedded/student_template.xlsx
var studentTemplate embed.FS

func parseInt64Param(r *http.Request, name string) (int64, error) {
	v := r.PathValue(name)
	return strconv.ParseInt(v, 10, 64)
}

func (a *App) ListStudents(w http.ResponseWriter, r *http.Request) {
	students, err := models.ListStudents(a.DB)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "students": students})
}

func (a *App) CreateStudent(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID   string `json:"id"`
		Name string `json:"stu_name"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	s, err := models.CreateStudent(a.DB, req.ID, req.Name)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	log.Printf("[学生] 新增 %q (学号: %s)", s.Name, s.ID)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "student": s})
}

func (a *App) UpdateStudent(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "无效的学生ID")
		return
	}
	var req struct {
		Name string `json:"stu_name"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if err := models.UpdateStudent(a.DB, id, req.Name); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	log.Printf("[学生] 更新 ID=%q -> %q", id, req.Name)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (a *App) DeleteStudent(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "无效的学生ID")
		return
	}
	var singleCount, multiCount int
	if err := a.DB.QueryRow(`SELECT COUNT(*) FROM ownership_single_subrecords WHERE student_id = ?`, id).Scan(&singleCount); err != nil {
		writeError(w, http.StatusInternalServerError, "检查常规扣分关联失败: "+err.Error())
		return
	}
	if err := a.DB.QueryRow(`SELECT COUNT(*) FROM ownership_multi_subrecords WHERE student_id = ?`, id).Scan(&multiCount); err != nil {
		writeError(w, http.StatusInternalServerError, "检查寝室整体差子项关联失败: "+err.Error())
		return
	}
	if singleCount > 0 || multiCount > 0 {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("该学生仍被 %d 条常规扣分认定和 %d 条寝室整体差子项关联，不能删除", singleCount, multiCount))
		return
	}
	if err := models.DeleteStudent(a.DB, id); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	log.Printf("[学生] 删除 ID=%q", id)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (a *App) BatchDeleteStudents(w http.ResponseWriter, r *http.Request) {
	var req struct {
		IDs []string `json:"ids"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if len(req.IDs) == 0 {
		writeError(w, http.StatusBadRequest, "请选择要删除的学生")
		return
	}

	tx, err := a.DB.Begin()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "创建删除事务失败: "+err.Error())
		return
	}
	defer tx.Rollback()

	deleted := 0
	for _, rawID := range req.IDs {
		id := strings.TrimSpace(rawID)
		if id == "" {
			writeError(w, http.StatusBadRequest, "存在无效的学生ID")
			return
		}
		var singleCount, multiCount int
		if err := tx.QueryRow(`SELECT COUNT(*) FROM ownership_single_subrecords WHERE student_id = ?`, id).Scan(&singleCount); err != nil {
			writeError(w, http.StatusInternalServerError, "检查常规扣分关联失败: "+err.Error())
			return
		}
		if err := tx.QueryRow(`SELECT COUNT(*) FROM ownership_multi_subrecords WHERE student_id = ?`, id).Scan(&multiCount); err != nil {
			writeError(w, http.StatusInternalServerError, "检查寝室整体差子项关联失败: "+err.Error())
			return
		}
		if singleCount > 0 || multiCount > 0 {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("学生 %s 仍被 %d 条常规扣分认定和 %d 条寝室整体差子项关联，不能删除", id, singleCount, multiCount))
			return
		}
		res, err := tx.Exec(`DELETE FROM students WHERE id = ?`, id)
		if err != nil {
			writeError(w, http.StatusBadRequest, "删除学生失败: "+err.Error())
			return
		}
		n, _ := res.RowsAffected()
		deleted += int(n)
	}
	if err := tx.Commit(); err != nil {
		writeError(w, http.StatusInternalServerError, "提交删除事务失败: "+err.Error())
		return
	}
	log.Printf("[学生] 批量删除 %d 名学生", deleted)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "deleted": deleted})
}

func (a *App) ImportStudents(w http.ResponseWriter, r *http.Request) {
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

	rows, err := f.GetRows(f.GetSheetName(0))
	if err != nil {
		writeError(w, http.StatusBadRequest, "读取工作表失败")
		return
	}
	if len(rows) < 2 {
		writeError(w, http.StatusBadRequest, "Excel 文件中没有数据行（除表头外至少需要一行数据）")
		return
	}

	headerRow := rows[0]
	nameCol, idCol := -1, -1
	for i, h := range headerRow {
		h = strings.TrimSpace(h)
		if h == "姓名" {
			nameCol = i
		} else if h == "学号" {
			idCol = i
		}
	}
	if nameCol < 0 || idCol < 0 {
		writeError(w, http.StatusBadRequest, "表头必须包含「姓名」和「学号」列")
		return
	}

	studentIDPattern := regexp.MustCompile(`^\d+$`)
	var imported []models.Student
	var errors []string

	for i := 1; i < len(rows); i++ {
		row := rows[i]
		name := ""
		studentID := ""
		if nameCol < len(row) {
			name = strings.TrimSpace(row[nameCol])
		}
		if idCol < len(row) {
			studentID = strings.TrimSpace(row[idCol])
		}
		if name == "" && studentID == "" {
			continue
		}
		if name == "" {
			errors = append(errors, fmt.Sprintf("第 %d 行: 姓名为空", i+1))
			continue
		}
		if studentID == "" {
			errors = append(errors, fmt.Sprintf("第 %d 行: 学号为空", i+1))
			continue
		}
		if !studentIDPattern.MatchString(studentID) {
			errors = append(errors, fmt.Sprintf("第 %d 行: 学号 %q 格式不正确（仅限数字）", i+1, studentID))
			continue
		}
		s, err := models.CreateStudent(a.DB, studentID, name)
		if err != nil {
			errors = append(errors, fmt.Sprintf("第 %d 行: %s", i+1, err.Error()))
			continue
		}
		imported = append(imported, *s)
	}

	result := map[string]any{"ok": true, "imported": len(imported), "students": imported}
	if len(errors) > 0 {
		result["errors"] = errors
	}
	log.Printf("[学生] 批量导入: 成功 %d 条, 失败 %d 条", len(imported), len(errors))
	writeJSON(w, http.StatusOK, result)
}

func (a *App) DownloadStudentTemplate(w http.ResponseWriter, r *http.Request) {
	f, err := studentTemplate.Open("embedded/student_template.xlsx")
	if err != nil {
		writeError(w, http.StatusInternalServerError, "模板文件不存在")
		return
	}
	defer f.Close()

	w.Header().Set("Content-Type", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet")
	w.Header().Set("Content-Disposition", "attachment; filename*=UTF-8''%E5%AD%A6%E7%94%9F%E4%BF%A1%E6%81%AF%E5%AF%BC%E5%85%A5-%E6%A8%A1%E6%9D%BF.xlsx")
	w.WriteHeader(http.StatusOK)
	io.Copy(w, f)
}
