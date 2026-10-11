package handlers

import (
	"bytes"
	_ "embed"
	"fmt"
	"math"
	"net/http"
	"sort"
	"strings"
	"time"

	"PoliceStyleWorkspace/models"

	"github.com/xuri/excelize/v2"
)

//go:embed embedded/monthly_summary_template.xlsx
var monthlySummaryTemplate []byte

// 月表（学生纪律作风记实考评扣分汇总表）版式：
//
//	第 1 行  标题（第<month>月…汇总表（<class>））
//	第 2 行  姓名 | 学号 | 扣分情况(合并 C:F)
//	第 3 行  日期 | 项目/情形 | 合计 | 签名
//	第 4 行起 每个学生一段：姓名/学号/合计/签名四列纵向合并，段内一行一条扣分
//
// 模板里的 <class> 换成 squad 表里的区队名，<month> 换成本次范围起始日期在
// 所属学期里的第几个月。
const (
	monthlySummarySheet    = "Sheet1"
	monthlySummaryFirstRow = 4
)

type monthlySummaryEntry struct {
	StudentID string
	Name      string
	Date      time.Time
	Item      string
	Share     float64
	// SchoolSupervision 为 true 表示来源记录 ID 以 xd_ 开头（校督扣分），
	// 该条的「项目/情形」单元格标红。
	SchoolSupervision bool
}

type monthlySummaryBlock struct {
	StudentID string
	Name      string
	Entries   []monthlySummaryEntry
}

// ExportMonthlySummary 生成指定日期区间的《学生纪律作风记实考评扣分汇总表》。
// 规则：只统计已认定学生的记录，且分值为 0 的条目不计入；来源记录 ID 以 xd_
// 开头的条目，其「项目/情形」单元格标红。
func (a *App) ExportMonthlySummary(w http.ResponseWriter, r *http.Request) {
	start, err := time.ParseInLocation("2006-01-02", strings.TrimSpace(r.URL.Query().Get("start")), time.Local)
	if err != nil {
		writeError(w, http.StatusBadRequest, "起始日期无效，应为 YYYY-MM-DD")
		return
	}
	end, err := time.ParseInLocation("2006-01-02", strings.TrimSpace(r.URL.Query().Get("end")), time.Local)
	if err != nil {
		writeError(w, http.StatusBadRequest, "结束日期无效，应为 YYYY-MM-DD")
		return
	}
	if end.Before(start) {
		writeError(w, http.StatusBadRequest, "结束日期不能早于起始日期")
		return
	}
	// 结束日期含当天，所以上界取次日零点
	endExclusive := end.AddDate(0, 0, 1)

	blocks, err := a.monthlySummaryBlocks(start, endExclusive)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	squad, err := models.GetSquadName(a.DB)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	file, err := excelize.OpenReader(bytes.NewReader(monthlySummaryTemplate))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "读取月表模板失败: "+err.Error())
		return
	}
	defer file.Close()

	// 标题里只替换区队名，月份保留模板原样的「第x月」（由使用者按当月情况手改）。
	title, _ := file.GetCellValue(monthlySummarySheet, "A1")
	title = strings.ReplaceAll(title, "<class>", squad)
	_ = file.SetCellValue(monthlySummarySheet, "A1", title)

	if err := writeMonthlySummarySheet(file, blocks); err != nil {
		writeError(w, http.StatusInternalServerError, "生成月表失败: "+err.Error())
		return
	}
	rangeText := start.Format("2006.1.2") + "-" + end.Format("2006.1.2")
	a.writeXLSX(w, file, fmt.Sprintf("学生纪律作风记实考评扣分汇总表（%s）%s.xlsx", squad, rangeText))
}

// monthlySummaryBlocks 按学生把区间内的扣分整理成分块结构，按学号排序。
func (a *App) monthlySummaryBlocks(start, end time.Time) ([]monthlySummaryBlock, error) {
	from, to := start.Format("2006-01-02 15:04:05"), end.Format("2006-01-02 15:04:05")
	entries := make([]monthlySummaryEntry, 0, 64)

	singleRows, err := a.DB.Query(`
		SELECT o.student_id, s.stu_name, r.submit_date, r.content, r.id, r.score,
		       (SELECT COUNT(*) FROM ownership_single_subrecords x WHERE x.record_id = r.id)
		FROM police_style_records_single_subrecords r
		JOIN ownership_single_subrecords o ON o.record_id = r.id
		JOIN students s ON s.id = o.student_id
		WHERE r.submit_date >= ? AND r.submit_date < ? AND r.score > 0
		ORDER BY o.student_id, r.submit_date, r.content`, from, to)
	if err != nil {
		return nil, fmt.Errorf("查询常规扣分失败: %w", err)
	}
	if err := func() error {
		defer singleRows.Close()
		for singleRows.Next() {
			var studentID, name, submitDate, content, recordID string
			var score float64
			var owners int
			if err := singleRows.Scan(&studentID, &name, &submitDate, &content, &recordID, &score, &owners); err != nil {
				return fmt.Errorf("读取常规扣分失败: %w", err)
			}
			if owners <= 0 {
				continue
			}
			date, ok := recordDate(submitDate)
			if !ok {
				continue
			}
			date = time.Date(date.Year(), date.Month(), date.Day(), 0, 0, 0, 0, date.Location())
			entries = append(entries, monthlySummaryEntry{
				StudentID: studentID, Name: name, Date: date, Item: content,
				Share: roundScore(score / float64(owners)),
				// 校督扣分的记录 ID 加 xd_ 前缀（见 isSchoolSupervisionDate 的逐行判定）
				SchoolSupervision: strings.HasPrefix(recordID, "xd_"),
			})
		}
		return singleRows.Err()
	}(); err != nil {
		return nil, err
	}

	multiRows, err := a.DB.Query(`
		SELECT o.student_id, st.stu_name, r.submit_date, r.dorm_name, r.content, sub.content, r.id, r.score,
		       (SELECT COUNT(*) FROM subrecords_for_police_style_records_multi_subrecords x WHERE x.belongs_to = r.id),
		       (SELECT COUNT(*) FROM ownership_multi_subrecords y WHERE y.subrecord_id = sub.id)
		FROM police_style_records_multi_subrecords r
		JOIN subrecords_for_police_style_records_multi_subrecords sub ON sub.belongs_to = r.id
		JOIN ownership_multi_subrecords o ON o.subrecord_id = sub.id
		JOIN students st ON st.id = o.student_id
		WHERE r.submit_date >= ? AND r.submit_date < ? AND r.score > 0
		ORDER BY o.student_id, r.submit_date, r.content`, from, to)
	if err != nil {
		return nil, fmt.Errorf("查询寝室整体差失败: %w", err)
	}
	if err := func() error {
		defer multiRows.Close()
		for multiRows.Next() {
			var studentID, name, submitDate, dormName, content, subContent, recordID string
			var score float64
			var subs, owners int
			if err := multiRows.Scan(&studentID, &name, &submitDate, &dormName, &content, &subContent, &recordID, &score, &subs, &owners); err != nil {
				return fmt.Errorf("读取寝室整体差失败: %w", err)
			}
			if subs <= 0 || owners <= 0 {
				continue
			}
			date, ok := recordDate(submitDate)
			if !ok {
				continue
			}
			date = time.Date(date.Year(), date.Month(), date.Day(), 0, 0, 0, 0, date.Location())
			item := strings.TrimSpace(dormName + " " + content)
			if sub := strings.TrimSpace(subContent); sub != "" {
				item += " / " + sub
			}
			entries = append(entries, monthlySummaryEntry{
				StudentID: studentID, Name: name, Date: date, Item: item,
				Share:             roundScore(score / float64(subs) / float64(owners)),
				SchoolSupervision: strings.HasPrefix(recordID, "xd_"),
			})
		}
		return multiRows.Err()
	}(); err != nil {
		return nil, err
	}

	sort.SliceStable(entries, func(i, j int) bool {
		if entries[i].StudentID != entries[j].StudentID {
			return entries[i].StudentID < entries[j].StudentID
		}
		if !entries[i].Date.Equal(entries[j].Date) {
			return entries[i].Date.Before(entries[j].Date)
		}
		return entries[i].Item < entries[j].Item
	})

	blocks := make([]monthlySummaryBlock, 0, len(entries))
	for _, entry := range entries {
		if len(blocks) == 0 || blocks[len(blocks)-1].StudentID != entry.StudentID {
			blocks = append(blocks, monthlySummaryBlock{StudentID: entry.StudentID, Name: entry.Name})
		}
		last := &blocks[len(blocks)-1]
		last.Entries = append(last.Entries, entry)
	}
	return blocks, nil
}

// writeMonthlySummarySheet 把分块数据写进模板：第 4 行起步，姓名/学号/合计/签名
// 四列按学生纵向合并；校督（来源 ID 以 xd_ 开头）的「项目/情形」单元格标红。
func writeMonthlySummarySheet(file *excelize.File, blocks []monthlySummaryBlock) error {
	sheet := monthlySummarySheet
	patternRow := monthlySummaryFirstRow

	styleID := map[string]int{}
	styleVal := map[string]*excelize.Style{}
	for _, col := range []string{"A", "B", "C", "D", "E", "F"} {
		id, style, err := monthlyCellStyle(file, cellRef(col, patternRow))
		if err != nil {
			return err
		}
		styleID[col], styleVal[col] = id, style
	}
	// 日期列统一 yyyy/m/d，合计列 0.###，校督条目「项目/情形」用红字
	dateStyle := monthlyDerivedStyle(file, styleVal["C"], styleID["C"], func(s *excelize.Style) {
		setMonthlyNumFmt(s, "yyyy/m/d")
	})
	scoreStyle := monthlyDerivedStyle(file, styleVal["E"], styleID["E"], func(s *excelize.Style) {
		setMonthlyNumFmt(s, "0.###")
	})
	redStyle := monthlyDerivedStyle(file, styleVal["D"], styleID["D"], func(s *excelize.Style) {
		if s.Font == nil {
			s.Font = &excelize.Font{}
		}
		s.Font.Color = "FF0000"
	})

	height := 14.25
	if h, err := file.GetRowHeight(sheet, patternRow); err == nil && h > 0 {
		height = h
	}

	row := monthlySummaryFirstRow
	for _, block := range blocks {
		first := row
		total := 0.0
		for _, entry := range block.Entries {
			total += entry.Share
			_ = file.SetCellValue(sheet, cellRef("A", row), block.Name)
			_ = file.SetCellValue(sheet, cellRef("B", row), block.StudentID)
			_ = file.SetCellValue(sheet, cellRef("C", row), entry.Date)
			_ = file.SetCellValue(sheet, cellRef("D", row), entry.Item)
			for _, col := range []string{"A", "B", "F"} {
				_ = file.SetCellStyle(sheet, cellRef(col, row), cellRef(col, row), styleID[col])
			}
			_ = file.SetCellStyle(sheet, cellRef("C", row), cellRef("C", row), dateStyle)
			dStyle := styleID["D"]
			if entry.SchoolSupervision {
				dStyle = redStyle
			}
			_ = file.SetCellStyle(sheet, cellRef("D", row), cellRef("D", row), dStyle)
			_ = file.SetRowHeight(sheet, row, height)
			row++
		}
		last := row - 1
		_ = file.SetCellValue(sheet, cellRef("E", first), roundScore(total))
		for r := first; r <= last; r++ {
			_ = file.SetCellStyle(sheet, cellRef("E", r), cellRef("E", r), scoreStyle)
			_ = file.SetRowHeight(sheet, r, height)
		}
		if last > first {
			for _, col := range []string{"A", "B", "E", "F"} {
				if err := file.MergeCell(sheet, cellRef(col, first), cellRef(col, last)); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func cellRef(col string, row int) string { return fmt.Sprintf("%s%d", col, row) }

func setMonthlyNumFmt(style *excelize.Style, format string) {
	style.NumFmt = 0
	style.CustomNumFmt = &format
}

// monthlyCellStyle 读取模板单元格的样式。模板是 Excel 存出来的，空填充会写成
// patternType="none"、被 excelize 解成 Pattern=-1，而 NewStyle 只接受 0..18，
// 直接回写会报 “fill pattern value must be between 0 and 18”；这里把越界的
// 填充值归零（等于无填充，外观不变）后再交给 NewStyle。
func monthlyCellStyle(file *excelize.File, ref string) (int, *excelize.Style, error) {
	id, err := file.GetCellStyle(monthlySummarySheet, ref)
	if err != nil {
		return 0, nil, err
	}
	style, err := file.GetStyle(id)
	if err != nil || style == nil {
		return id, nil, err
	}
	if style.Fill.Pattern < 0 || style.Fill.Pattern > 18 {
		style.Fill.Pattern = 0
	}
	if style.Font != nil {
		style.Font.Color = monthlyFontColor(style.Font.Color)
	}
	for i := range style.Border {
		style.Border[i].Color = monthlyFontColor(style.Border[i].Color)
	}
	return id, style, nil
}

// monthlyFontColor 把字体颜色统一成 6 位 RGB。excelize 读 xlsx 时会把
// rgb="FF000000" 解成 "000000"（丢掉 alpha），写回时它自己再补 alpha；如果这里
// 传 8 位，它就会写出 rgb="FFFF000000" 这种非法值，Excel / openpyxl 都打不开。
// theme 色读出来是空串，保持原样让 ColorTheme 生效。
func monthlyFontColor(value string) string {
	v := strings.ToUpper(strings.TrimSpace(strings.TrimPrefix(value, "#")))
	if len(v) == 8 {
		return v[2:]
	}
	return v
}

// monthlyDerivedStyle 复制模板样式、按 mutate 改一处后注册成新样式。
func monthlyDerivedStyle(file *excelize.File, base *excelize.Style, fallback int, mutate func(*excelize.Style)) int {
	if base == nil {
		return fallback
	}
	style := cloneMonthlyStyle(base)
	mutate(style)
	id, err := file.NewStyle(style)
	if err != nil {
		return fallback
	}
	return id
}

// cloneMonthlyStyle 深拷贝会被改动的字段（Font / Fill），其余按值复制即可。
func cloneMonthlyStyle(base *excelize.Style) *excelize.Style {
	out := *base
	if base.Font != nil {
		font := *base.Font
		out.Font = &font
	}
	out.Fill = base.Fill
	out.Fill.Color = append([]string(nil), base.Fill.Color...)
	if base.Alignment != nil {
		align := *base.Alignment
		out.Alignment = &align
	}
	if base.Protection != nil {
		protection := *base.Protection
		out.Protection = &protection
	}
	if base.Border != nil {
		out.Border = append([]excelize.Border(nil), base.Border...)
	}
	return &out
}

func roundScore(value float64) float64 {
	return math.Round(value*1e6) / 1e6
}
