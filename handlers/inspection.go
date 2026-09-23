package handlers

import (
	"errors"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"
	"time"

	"PoliceStyleWorkspace/models"

	"github.com/shakinm/xlsReader/xls"
)

// 信网大队《警务化管理日常检查表》（学校下发的是老式 .xls，excelize 读不了，
// 用 shakinm/xlsReader 读成 [][]string 后走和 xlsx 一样的解析流程）。
//
// 版式：
//
//	第 1 行  信网大队警务化管理日常检查表\n检查日期：2026年9月22日
//	第 2 行  区队 | 内务组 | 扣分分值 | 警容风纪组 | 扣分分值 | 生活秩序 | 扣分分值 | 总扣分
//	第 3 行起 每个区队占一段（区队名只出现在该段首行的"区队"列，向下沿用），
//	         三段内容列分别为 内务组 / 警容风纪组 / 生活秩序，右侧一列是该项扣分分值。
var (
	inspectionTitlePattern = regexp.MustCompile(`警务化管理日常检查表`)
	inspectionDatePattern  = regexp.MustCompile(`检查日期\s*[:：]\s*(\d{4})\s*年\s*(\d{1,2})\s*月\s*(\d{1,2})\s*日`)
)

// 行内的时间关键词。识别时取"出现位置最靠前"的那个；同一位置优先更长的
// （晚点名前 要先于 晚点名）。
var inspectionTimeKeywords = []string{
	"晚点名前", "晚点名", "上午", "早操", "早一", "早二", "下午", "中午", "傍晚", "晚上",
}

// inspectionGroupColumns 是三个检查组在表头里的名字，顺序即优先级。
var inspectionGroupNames = []string{"内务组", "警容风纪组", "生活秩序"}

// readXLSRows 把 .xls（BIFF8）读成和 excelize.GetRows 一样的 [][]string。
func readXLSRows(reader io.ReadSeeker) ([][]string, error) {
	workbook, err := xls.OpenReader(reader)
	if err != nil {
		return nil, fmt.Errorf("无法读取 .xls 文件: %w", err)
	}
	if workbook.GetNumberSheets() == 0 {
		return nil, errors.New("工作簿里没有工作表")
	}
	sheet, err := workbook.GetSheet(0)
	if err != nil {
		return nil, fmt.Errorf("读取工作表失败: %w", err)
	}
	rows := make([][]string, 0, sheet.GetNumberRows())
	for i := 0; i < sheet.GetNumberRows(); i++ {
		row, err := sheet.GetRow(i)
		if err != nil || row == nil {
			rows = append(rows, nil)
			continue
		}
		cols := row.GetCols()
		cells := make([]string, len(cols))
		for j, cell := range cols {
			if cell == nil {
				continue
			}
			cells[j] = cell.GetString()
		}
		rows = append(rows, cells)
	}
	return rows, nil
}

// parseInspectionRows 解析《警务化管理日常检查表》：只取已配置区队那一段的
// 内务组 / 警容风纪组 / 生活秩序 内容。ok=false 表示不是这种版式。
func (a *App) parseInspectionRows(rows [][]string) ([]parsedDeductionRow, []string, bool) {
	titleRow := -1
	for i := 0; i < len(rows) && i < 5; i++ {
		for _, cell := range rows[i] {
			if inspectionTitlePattern.MatchString(normalizeExcelHeader(cell)) {
				titleRow = i
				break
			}
		}
		if titleRow >= 0 {
			break
		}
	}
	if titleRow < 0 {
		return nil, nil, false
	}

	checkDate, err := inspectionCheckDate(rows[titleRow])
	if err != nil {
		return nil, []string{err.Error()}, true
	}
	headerRow, squadCol, groupCols := findInspectionHeader(rows, titleRow)
	if headerRow < 0 {
		return nil, []string{"未找到「区队 / 内务组 / 警容风纪组 / 生活秩序」表头"}, true
	}

	squad, err := models.GetSquadName(a.DB)
	if err != nil {
		return nil, []string{err.Error()}, true
	}
	squad = normalizeExcelHeader(squad)
	if squad == "" {
		return nil, []string{"未配置区队名称，请先在「工作台 → 区队」中设置"}, true
	}

	students, err := models.ListStudents(a.DB)
	if err != nil {
		return nil, []string{err.Error()}, true
	}
	// 姓名匹配要长的优先：否则「张三丰」会被「张三」先匹配掉
	sort.SliceStable(students, func(i, j int) bool {
		return len([]rune(students[i].Name)) > len([]rune(students[j].Name))
	})

	// 时分秒取当前时刻，日期用表里的「检查日期」
	clock := time.Now().Format("15:04:05")
	submitDate := checkDate + " " + clock

	parsed := make([]parsedDeductionRow, 0, 32)
	seen := make(map[string]struct{})
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
		if value := get(squadCol); value != "" {
			currentSquad = value
		}
		if currentSquad != squad {
			continue
		}
		squadSeen = true
		for _, group := range groupCols {
			content := get(group.contentCol)
			if content == "" {
				continue
			}
			score := inspectionScore(get(group.scoreCol))
			name, item := splitInspectionItem(content, students, squad)
			if name == "" || item == "" {
				continue
			}
			// 同一张表里完全相同的条目只导一次，避免同秒生成同一个记录 ID 而报重复
			key := name + "\x1f" + item + "\x1f" + fmt.Sprintf("%g", score)
			if _, dup := seen[key]; dup {
				continue
			}
			seen[key] = struct{}{}
			parsed = append(parsed, parsedDeductionRow{
				RowNumber:   i + 1,
				SubmitDate:  submitDate,
				StudentName: name,
				Content:     item,
				Score:       score,
			})
		}
	}

	errs := make([]string, 0, 1)
	if !squadSeen {
		errs = append(errs, fmt.Sprintf("表格中未找到区队 %q 的扣分记录", squad))
	}
	return parsed, errs, true
}

// inspectionCheckDate 从标题里取「检查日期：2026年9月22日」并转成 YYYY-MM-DD。
func inspectionCheckDate(titleRow []string) (string, error) {
	for _, cell := range titleRow {
		matches := inspectionDatePattern.FindStringSubmatch(normalizeExcelHeader(cell))
		if matches == nil {
			continue
		}
		year, month, day := matches[1], matches[2], matches[3]
		parsed, err := time.ParseInLocation("2006-1-2", fmt.Sprintf("%s-%s-%s", year, month, day), time.Local)
		if err != nil {
			return "", fmt.Errorf("检查日期 %s年%s月%s日 不是有效日期", year, month, day)
		}
		return parsed.Format("2006-01-02"), nil
	}
	return "", errors.New("表头没有找到「检查日期：x年x月x日」")
}

type inspectionGroupColumn struct {
	contentCol int
	scoreCol   int
}

// findInspectionHeader 定位表头行，并给出「区队」列以及三个检查组的内容/分值列。
func findInspectionHeader(rows [][]string, titleRow int) (headerRow, squadCol int, groups []inspectionGroupColumn) {
	limit := titleRow + 5
	if limit > len(rows) {
		limit = len(rows)
	}
	for i := titleRow; i < limit; i++ {
		squadCol = -1
		found := map[string]int{}
		for colIndex, value := range rows[i] {
			header := normalizeExcelHeader(value)
			flat := strings.NewReplacer("\n", "", "\r", "", " ", "", "\u3000", "").Replace(header)
			switch {
			case header == "区队":
				squadCol = colIndex
			case flat == "内务组" || flat == "警容风纪组" || flat == "生活秩序":
				found[flat] = colIndex
			}
		}
		if squadCol < 0 || len(found) == 0 {
			continue
		}
		groups = groups[:0]
		for _, name := range inspectionGroupNames {
			contentCol, ok := found[name]
			if !ok {
				continue
			}
			groups = append(groups, inspectionGroupColumn{
				contentCol: contentCol,
				scoreCol:   inspectionScoreColumn(rows[i], contentCol),
			})
		}
		if len(groups) > 0 {
			return i, squadCol, groups
		}
	}
	return -1, -1, nil
}

// inspectionScoreColumn 找内容列右侧那一列「扣分分值」；表头不好认时退回 contentCol+1。
func inspectionScoreColumn(header []string, contentCol int) int {
	for col := contentCol + 1; col < len(header); col++ {
		flat := strings.NewReplacer("\n", "", "\r", "", " ", "", "\u3000", "").Replace(normalizeExcelHeader(header[col]))
		if strings.Contains(flat, "分值") {
			return col
		}
		if flat == "总扣分" {
			break
		}
	}
	return contentCol + 1
}

// inspectionScore 把「扣分分值」列转成分数：值可能是 0.1 这种数字，也可能是
// 「提醒」（提醒不扣分，记 0）。
func inspectionScore(value string) float64 {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0
	}
	var score float64
	if _, err := fmt.Sscanf(value, "%f", &score); err != nil {
		return 0
	}
	if score < 0 {
		score = -score
	}
	return score
}

// splitInspectionItem 把一行检查内容拆成「姓名」和「扣分项目」，规则（示例里的
// 姓名与编号都是占位值，真实姓名只在导入时匹配本地 students 表）：
//
//  1. 行首就是学生姓名 → 姓名=该姓名，其余为内容
//     （张三 上午 腰带摆放不规范 / 李四早二阳台晾衣杆摆放有问题）
//  2. 行首是时间关键词 → 整条没有归属，姓名记区队名，内容为整行
//     （早二包干区脏 / 早二 10001消防箱上有杂物）
//  3. 时间关键词出现在中间 → 关键词之前的部分当姓名（能认出学生就用学生名）
//     （10002 早二 厕所灯未关 / 10003孙七上午地面脏）
//  4. 没有时间关键词 → 按第一个空白切分（赵六 滑柜未完全贴进 / 周八 水箱门未关）
//  5. 整行既没有姓名也没有时间关键词、也没有分隔符 → 归到区队，内容为整行
//
// 姓名字段只是原样记下，能不能认到学生由后续 persistDeductionRows 按姓名匹配，
// 匹配不到就是「未认定」。
func splitInspectionItem(raw string, students []models.Student, squad string) (string, string) {
	line := strings.TrimSpace(raw)
	if line == "" {
		return "", ""
	}

	for _, student := range students {
		if student.Name == "" {
			continue
		}
		if strings.HasPrefix(line, student.Name) {
			return student.Name, inspectionItemText(strings.TrimSpace(line[len(student.Name):]), line)
		}
	}

	index, _ := firstInspectionKeyword(line)
	switch {
	case index == 0:
		return squad, line
	case index > 0:
		prefix := trimInspectionName(line[:index])
		if name := matchStudentNameInText(prefix, students); name != "" {
			prefix = name
		}
		if prefix == "" {
			prefix = squad
		}
		return prefix, inspectionItemText(strings.TrimSpace(line[index:]), line)
	}

	if cut := strings.IndexAny(line, " \t\u3000"); cut > 0 {
		prefix := trimInspectionName(line[:cut])
		if name := matchStudentNameInText(prefix, students); name != "" {
			prefix = name
		}
		return prefix, inspectionItemText(strings.TrimSpace(line[cut:]), line)
	}

	return squad, line
}

// firstInspectionKeyword 返回时间关键词在行里最早出现的位置；同一位置取更长的词。
func firstInspectionKeyword(line string) (int, int) {
	bestIndex, bestLength := -1, 0
	for _, keyword := range inspectionTimeKeywords {
		index := strings.Index(line, keyword)
		if index < 0 {
			continue
		}
		if bestIndex == -1 || index < bestIndex || (index == bestIndex && len(keyword) > bestLength) {
			bestIndex, bestLength = index, len(keyword)
		}
	}
	return bestIndex, bestLength
}

// matchStudentNameInText 在片段里找学生姓名，取最长的那个（用于「10003孙七」这种
// 房间号/编号与姓名连写的情况）。
func matchStudentNameInText(text string, students []models.Student) string {
	best := ""
	for _, student := range students {
		if student.Name == "" || len([]rune(student.Name)) <= len([]rune(best)) {
			continue
		}
		if strings.Contains(text, student.Name) {
			best = student.Name
		}
	}
	return best
}

// trimInspectionName 去掉姓名片段两端的空白与标点。
func trimInspectionName(value string) string {
	return strings.Trim(strings.TrimSpace(value), "，,、;；:：.。 \t\u3000")
}

// inspectionItemText 收尾：内容为空时退回整行，避免把整行都吞进姓名字段。
func inspectionItemText(rest, original string) string {
	if rest == "" {
		return strings.TrimSpace(original)
	}
	return rest
}
