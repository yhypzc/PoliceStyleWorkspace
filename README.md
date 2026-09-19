# 纪检工作台 / PoliceStyleWorkspace 技术文档

# **纪检工作台 / PoliceStyleWorkspace**

纪检工作台是一个面向警务化管理日常工作和扣分记录维护的本地化桌面 Web 应用。项目由 Go 单体服务、内嵌 Vue 前端和 EUI\-NEO Windows GUI 管理端组成，运行数据默认保存在发布目录内。

## **功能**

### 申诉模板导出

常规扣分记录、寝室整体差记录均可打开申诉窗口：填写大队/区队与复议说明、上传或删除点名扣分照片与申诉照片后，调用 Word 插入图片生成 `.doc` 申诉文档，并连同证据照片打包成 zip；单条导出与批量汇总均支持。

### 扣分记录管理

学生信息、学期、寝室、常规扣分记录、寝室整体差记录管理，具备增删改查基本功能。

常规扣分记录管理与寝室整体差扣分记录管理右上角均有「添加项目」按钮，用手工录入一条记录，不必走 Excel：

- 常规扣分「添加项目」：姓名、日期（日历 + 时刻表，默认打开时取当前日期与时刻）、认定（在学生列表中多选，可搜学号/姓名）、扣分内容、分数、是否计入区队周扣分、扣分类型（大队督察扣分／校督扣分）。`姓名` 留空时按「认定」的学生姓名自动拼接；姓名与认定至少要有一个。扣分类型选校督时记录 ID 加 `xd_` 前缀，申诉导出即走校督模板。提交到 `POST /api/deductions`，入库与认定关联在同一事务内完成（`models.CreateDeductionRecordWithOwnership`）。
- 寝室整体差「添加项目」：日期、寝室名称、扣分项目、分数。日期按天存为 `YYYY-MM-DD 00:00:00`，与 Excel 导入保持一致；分数不可为负。提交到 `POST /api/multi-deductions`（`models.CreateMultiDeductionRecord`）。

### 区队名称与每日通报表导入

工作台第一个卡片为「区队」，显示当前区队名称（默认 `24网安二`），点击后在弹窗中修改；名称存放在单列表 `squad(squad_name)` 中，由 `models.CreateSquadTable` 在初始化时建表并写入默认值，接口为 `GET /api/squad` 与 `PUT /api/squad`。

常规扣分记录的「导入 Excel」除导入模板外，还兼容信网学院每日下发的《警务化管理扣分表》（`.data/` 下样例），识别逻辑见 `handlers/deduction.go:parseMassNoticeRows`：

- 表首标题形如「信网学院日警务化管理通报结果（9月7日）」，取其中 `a月b日` 与当前年份拼成 `YYYY-MM-DD`。
- 表头为 `序号 | 区队 | 姓名 | 时间 | 轻微违纪违规行为 | 建议扣分 | 总扣分`；`姓名`→记录姓名（按姓名智能匹配认定学生），`轻微违纪违规行为`→扣分项目，`建议扣分`→分数。
- 只导入 `区队` 与已配置区队名称一致的行（区队名只在每个区队块的首行出现，会向后沿用）；表格中没有该区队时给出提示。
- `时间` 列（上午/下午）与程序按当前时刻判定的时间段比较：一致时 `hh:mm:ss` 取当前时刻，不一致时上午记 `08:00:00`、下午记 `18:00:00`。
- 记录按常规（大队督察）规则生成 ID，不做校督转换（校督扣分请用导入模板，类型逐行判定，见「常规扣分导入的校督／大队督察判定」）。

### 常规扣分导入的校督／大队督察判定

「导入 Excel」使用 `handlers/embedded/deduction_template.xlsx`，表头为 `日期 | 姓名 | 扣分项目 | 分数 | 违规学号`（`学号` 或 `违规学号` 列必需）。扣分类型**逐行判定**，依据是该行「日期」的写法，见 `handlers/deduction.go:isSchoolSupervisionDate`：

- 该行日期写成「月.日」（如 `9.7`、`10.12`，正则 `^\d{1,2}\.\d{1,2}$`）→ **校督扣分**：`schoolSupervisionDate` 补当前年份成 `YYYY-MM-DD 00:00:00`，负分取绝对值，记录 ID 加 `xd_` 前缀。
- 其余写法（完整时间戳 `YYYY-MM-DD HH:MM:SS` 等）→ **大队督察扣分**：日期原样入库，分数保留符号，ID 为纯 md5。

补充规则：

- 同一张表允许校督行与大队督察行**混排**，互不影响。
- 日期为空的行沿用**上一行**的判定类型，再按该类型补当前时间（兼容表内合并单元格）；日期列整体缺失时全部按大队督察处理。
- 前端申诉窗口按记录 ID 前缀判断类型：`row.id.startsWith('xd_')` → 校督申诉模板（导出分类 `校督`），否则大队督察申诉模板（导出分类 `大队督察`）。因此记录 ID 前缀与逐行判定必须成对使用。
- 每日「警务化管理通报结果」表格走 `parseMassNoticeRows` 分支，永远按常规（大队督察）入库，不做校督转换。

### 每周扣分展示与导出

日常综合管理按周次展示每日分数，支持导出。

「本周扣分条目汇总」按常规扣分/寝室整体差分区列出该周全部记录：常规扣分可直接编辑、删除，寝室整体差可编辑、删除并进入子项管理；改动后自动刷新周详情与条目列表。

学期总表动态统计，支持导出。

### 包干区自动轮值

工作台展示当前学期、轮值包干区寝室

- 当前轮值寝室根据当前日期所在学期周序计算：`weekIndex = floor((now - semesterStart) / 7天)`，`dutySeq = weekIndex % dormCount + 1`，再按寝室 `seq` 匹配。

### 未指定条目提醒

未指定条目、无子项寝室整体差等统计卡片，并可点击查看明细。

- 未指定条目包括未认定学生的常规扣分记录，以及存在未分配负责学生子项的寝室整体差记录。

- 无子项寝室整体差通过 `NOT EXISTS` 检查主记录是否没有任何子项。

### **扣分实时计算**

警务化扣分统计代码集中在 `handlers/workspace.go`。

工作台统计：

- `WorkspaceStats` 统计常规扣分条数、寝室整体差条数、无子项寝室整体差条数、总扣分、未指定条目、学期外记录和当前轮值寝室。

- 总扣分为常规扣分表 `SUM(score)` 加寝室整体差表 `SUM(score)`。

日常综合管理分数计算在 `fillDailyScores`：

- 常规扣分：一条记录如果认定了 N 名学生，每名学生承担 `record.score / N`。分摊前先检查该记录的"是否计入区队周扣分"（`include_weekly`）：为 `0`（不计入）时整条记录跳过，不参与任何周统计。
- 寝室整体差：一条主记录如果有 M 个子项，某子项分配给 N 名学生，则该子项每名学生承担 `record.score / M / N`。寝室整体差恒为计入。

- 周统计按日期聚合到 `student_id + day`；学期汇总再按周累加，最终按总扣分降序展示。

- 学期汇总（`DailyManagementSummary` / `ExportDailyManagementSummary`）忽略"是否计入区队周扣分"这一选项，统计全部记录；只有周维度（周详情、周导出、周报）才排除不计入的记录。

- 导出明细使用同一分摊逻辑，代码见 `fillDailyExportDetails`。周导出的 sheet2 明细/学期汇总的"扣分记录"表照常列出该周全部记录（含寝室整体差子项），并带一列"是否计入区队周扣分"（是/否，寝室整体差恒为是）。

- **未认定的记录也会列出**（`deductionDetailRows`），三类没有归属的行姓名列统一显示「未认定」（常规扣分记录里本身存了原始姓名时优先显示那个姓名）：
  - 常规扣分没有任何认定学生 → 整条记录的分数记成一行；
  - 寝室整体差子项没有负责学生 → 按 `record.Score / 子项数` 记成一行；
  - 寝室整体差主记录**连子项都没有**（"无子项"）→ 整条记录的分数记成一行。

  所以明细表分数列的合计就等于该周列出的全部扣分项之和，与 `include_weekly`、认定状态无关。

- 周导出的 sheet2 明细在最后额外加一行**总计**：A 列写「总计」（与 sheet1 的总计行同一写法），**D 列（分数）填该周明细各行的分数合计**。这是"明细表自身的合计"，包含 `是否计入区队周扣分 = 否` 的行和未认定的行，因此可能与 sheet1 的总计（只算计入项、只算已认定学生）不同。

- 惩戒名单统计（`computePunishmentEntries`）同样在分摊前排除不计入的常规扣分记录。

### **惩戒名单统计**

按周对每名学生累计"计入惩戒分值"（LogicScore），当周累计达到阈值（默认 `0.3`）即进入惩戒名单，实现位于 `handlers/punishment.go`（`computePunishmentEntries`）。每条记录区分 `RawScore`（计入综测分值 = 应分摊原始分）与 `LogicScore`（计入惩戒分值 = 实际参与惩戒累计的分）。

时间窗口：给定学期与周序号 `weekIndex`，取 `[学期起始 + weekIndex×7 天, 学期起始 + (weekIndex+1)×7 天)`，末端截断到学期结束。周定义为某一周的周五到下一周的周四，因此学期起始日期为周五、结束日期为周四。

分摊与计入：

- 常规扣分记录：记录分 `S` 认定了 `N` 名学生，每名 `x = S/N`。计入 `logicScoreSingle(x, N)`：`N=1` 计 `x`；`N>1` 时若 `x<0.1` 计 `0`，否则计 `x`。
- 寝室整体差记录：主记录分 `S` 有 `M` 个子项、某子项指派 `N` 名负责学生，每名 `x = S/M/N`。计入 `logicScoreMulti(x, N)`：`N>1` 时 `x<0.1` 计 `0`，否则计 `x`；`N=1`（一人独责）时若 `x<0.1` 计 `0.1`（保底），否则计 `x`。

上榜判定：该生当周所有记录的 `LogicScore` 求和 `total ≥ 0.3` 即入惩戒名单。

微调（避免整体差"一人独责保底 0.1"把总分顶过阈值而误上榜）：对学生的"一人独责整体差子项"（`IsMulti && StudentIDs == 1`）把该项 `LogicScore` 放回其 `RawScore`（去掉保底）后重算 `total`——

- 独责项数 `≥3` 或 `0`：不调整；
- `=1`：放回该项 RawScore 后重算；
- `=2`：先放回 RawScore 较小的一项；若重算后 `total < 0.3`（默认阈值）则该生移出名单，否则再放回另一项。

重算后 `total` 仍 `≥ 阈值`者保留，否则移出。最终按 `total` 降序返回。每日播报周汇总复用同一套逻辑生成"预计惩戒名单"，并逐条列出记录的"计入综测/计入惩戒分值"与"是否惩戒"。

### **每日播报**

警务化管理每日播报支持 VPN 和内网系统抓取、钉钉 Webhook 机器人发送、手动补播、日志保存、日志导出和删除。

寝室信息支持可选手机号，用于每日播报中钉钉机器人真实 `atMobiles` @ 寝室长。

每日播报主流程在 `handlers/daily_report_scheduler.go`：

- `StartDailyReportScheduler` 每秒检查配置时间；到达时间后使用 `daily_report_auto_run.run_key` 保证同一天同一时间只自动执行一次。

- `runDailyReport` 读取播报配置和启用的钉钉机器人，调用 `fetchDailyReport` 抓取内容，再逐个机器人发送并写入 `daily_report_log`。

- `fetchDailyReport` 依次完成 VPN 登录、内网系统登录、按日期抓取扣分记录和未指定条目，最后调用 `formatDailyReportMessage` 生成播报文本。

- `formatDailyReportMessage` 会过滤已申诉记录、去重记录 ID、补充学期周次和本周包干区寝室。

- 当播报日期位于学期内，且为周五、周六或周日，并且轮值寝室配置了手机号时，正文末尾追加包干区任务提醒，同时返回 `atMobiles`。

- `postDingTalk` 使用钉钉文本消息格式发送；有手机号时 payload 包含 `at: { atMobiles, isAtAll:false }`，不是只在文本里拼接 `@`。

- 抓取原始响应通过 `daily_report_cache` 去重保存，日志通过 `raw_id` 引用；删除日志或机器人时会检查缓存是否仍被其他日志引用。

- 若播报配置开启自动入库（`daily_report_config.auto_import`），日志入库后会把当批抓取记录自动导入常规扣分表：复用"导出制表→导入解析"逻辑；申诉成功(`state=4`)与已删除(`state=7`)的记录被过滤，`violation_ids` 为空的整区/包干区记录按未指定条目导入。

- 播报日志导出 XLSX 的"违规学号"列会按"姓名→学号"自动填充（仅留能匹配到本地学生的），文件可直接回导。

### **定时通知管理**

定时通知用于按计划时间把一条自定义钉钉消息（如包干区打扫提醒）发送到指定机器人，页面入口为 `/report-events`，代码见 `models/report_event.go`、`handlers/report_event.go` 与 `handlers/report_event_scheduler.go`。界面文案统一使用「定时通知」，仅数据库表名与接口路径保留 `report_event` 前缀。

- 表格字段为通知 ID、计划播报时间、播报状态、预计发布内容、发送的机器人、日志、操作；右上角「新增定时通知」以与编辑相同的表单插入。

- 计划播报时间由日历表（`ElDatePicker`）与时刻表（`ElTimePicker`）拼成 `YYYY-MM-DD HH:MM:SS` 落库。

- `status` 为 `0` 表示还未播报、`1` 表示播报成功、`2` 表示播报失败，前端按状态显示标签，并据此把操作列的按钮在「测试」与「重试」之间切换。

- 操作列提供「编辑」（改时间、改内容、改机器人，保存后重置为未播报并清空日志）、「测试／重试」（立即发送并写入日志，失败时按钮显示「重试」）与「删除」。

- `StartReportEventScheduler` 每 15 秒扫描一次到期且未播报的通知并发送；发送串行执行，发送后立即写状态，因此重启不会重复发送。**服务停机期间错过的通知不做自动补播**：超过 10 分钟宽限窗后直接标记为「播报失败」并在日志写入原因，由人工点「重试」补发。

- 发送对象取自 `report_event_to_robots`，**忽略机器人自身的「启用/禁用」开关**——该开关只约束周报，因此被禁用的机器人同样会收到定时通知。

- 正文换行：内容按普通换行（`\n`）入库，前端表格用 `white-space: pre-wrap` 原样展示为相邻两行；发送时由 `handlers/report_event.go:markdownLineBreaks` 把每一行展开为独立段落（空行分隔）。钉钉 markdown 会丢掉单个 `\n`，只有空行才是真换行（[钉钉开发者社区](https://developer.aliyun.com/ask/515046)）。

- `@` 功能：编辑播报内容时输入 `@` 会弹出同学姓名下拉（`ElMention`，选项为「姓名（手机号）」），选中后插入 `@手机号`，支持 @ 多人。发送时 `handlers/report_event.go:reportEventAtMobiles` 从正文里提取 `@手机号`，并把 `@姓名` 也按 `students.phone_number` 解析成手机号，最终以钉钉 `markdown` 消息的 `at.atMobiles` 字段真实 @ 到人（正文必须保留 `@手机号`，钉钉才会渲染成 @）。

### **警务化管理周报**

每周五，每日播报在完成当日广播后，会额外向所有启用的钉钉机器人发送一份上一周（周五至周四）扣分周报，生成逻辑在 `handlers/daily_report_scheduler.go:formatWeeklySummaryMarkdown`。

- 触发与周窗口：仅当运行日为周五才生成（非周五直接返回，不发周报）；周定义为某一周的周五至下一周的周四，周五发送时由 `dailyReportPreviousWeekInfo` 取刚结束的上一周 `[周起始, 周结束)`，用 `fillDailyScores` 逐日计算每位学生每天的分摊分。

- 本周惩戒名单：复用"惩戒名单统计"（`computePunishmentEntries(weekStart, weekEnd, 0.3)`）得出名单，姓名先于正文列出；正文汇总表中名单成员整行以紫色粗体高亮。

- 汇总表：`姓名 | 学号 | 本周各日分值 | 合计`，仅保留本周总分非 0 的学生，按每人合计降序；表末 `合计` 行给出每日合计与周合计。

- 分值格式化（`formatDeductionScore`）：每个单元格中的非 0 分值，若为有限小数则保留全部位数（如 `0.0125`、`0.075`），若为无限循环小数则保留 3 位小数（如 `0.333`、`0.017`）。

- 惩戒明细：对名单成员逐条展开其记录，标注"计入惩戒分值"与"是否惩戒"，并列出每条记录的"计入综测分值/计入惩戒分值"。

## **登录与鉴权**

会话管理基于session。

登录入口为 `POST /api/login`，实现位于 `handlers/app.go:Login`：

- 请求体包含 `username`、`password`、`timestamp`，前端同时通过 `X-Login-Timestamp` 请求头传递时间戳作为兼容补充。

- 服务端校验用户名非空、时间戳非空且在允许时钟偏差内。

- `loginGuard` 记录同一来源、用户名、时间戳组合，已使用时间戳再次提交会被视为重放。

- 密码校验调用 `models.ValidateLogin`，仅允许 `admin` 用户，密码比较使用 `HashPassword(password, salt)`。

- 登录成功后创建 session 和 CSRF token，设置 `PSW_SESSION_ID` HttpOnly Cookie，并返回 `csrf_token`。

API 鉴权：

- 除 `/api/login` 外，业务 API 在 `main.go` 中统一包裹 `middleware.RequireAuth`。

- `RequireAuth` 先检查 `PSW_SESSION_ID` 是否存在、是否未过期、是否能在内存会话表中找到。

- 对 `POST`、`PUT`、`DELETE` 等非安全方法，额外校验请求头 `X-CSRF-Token` 必须等于当前 session 绑定的 CSRF token。

- 前端 `web/src/api.ts` 会从 `PSW_CSRF_TOKEN` Cookie 中读取 token，并自动为非 GET 请求设置 `X-CSRF-Token`。

- 未认证请求返回 `401`，CSRF 校验失败返回 `403`。

### 会话空闲超时

会话寿命是**空闲 10 分钟**（`main.go:sessionIdleTimeout`），不是登录后固定 10 分钟——只有用户真的在操作，截止时间才往后推：

- 服务端滑动续期：`middleware.RequireAuth` 在校验通过后把 `ExpiresAt` 重置为「现在 + 10 分钟」，并同步重发 Cookie（`Set-Cookie`），否则浏览器会先丢掉 Cookie 而服务端会话还活着。会话表里的过期项由 `cleanLoop` 每分钟清理。

- 后台轮询不算活跃：`GET /api/clock`（前端每 60 秒对时）走的是 `middleware.RequireAuthPassive`，只校验不续期，所以「页面开着但没人动」照样会到点掉线。

- 前端心跳：`App.vue` 监听 `mousedown`/`mousemove`/`keydown`/`wheel`/`touchstart`（5 秒节流），确有操作时最多每分钟调用一次 `POST /api/session/touch` 续期——避免「一直在页面上操作却没有发请求」被误判为空闲。

- 前端兜底：本地同时跑一个 10 分钟空闲计时器（初值取自 `/api/check-auth` 的 `idle_timeout_seconds`），到点直接提示并跳回登录页，不必等下一个请求返回 401。

- `GET /api/check-auth` 会返回 `idle_timeout_seconds` 与 `idle_remaining_seconds`，前端据此对齐自己的计时器。

## **安全性**

- 服务端默认只监听 `127.0.0.1:<port>`，避免对外网卡暴露。

- 登录接口要求请求携带时间戳，服务端基于时间窗口和已使用时间戳阻止重放。

- 登录失败计数采用固定 3 秒窗口：从第一次输错开始累计，3 秒到期清零；窗口内达到阈值后锁定 10 秒，并返回 `请求过于频繁`。

- 登录成功后发放 HttpOnly 会话 Cookie 和 CSRF Token；非 GET 认证接口要求携带 CSRF Token。

- 服务端使用系统互斥锁确保只有一个实例运行。

- 防止并发攻击和重放攻击：登录失败计数使用固定 3 秒窗口，窗口内达到阈值时锁定 10 秒并返回 `请求过于频繁`。

- 钉钉机器人接口POST采用5秒冷却机制，避免并发攻击导致信息轰炸。

敏感信息处理：

- VPN 密码、内网密码、钉钉 Webhook URL、钉钉加签密钥在数据库中保存真实值。

- 接口返回配置和机器人列表时只返回脱敏值；用户提交保存时仍写入真实值。相关处理在 `handlers/daily_report.go` 和 `models/daily_report.go`。

## **数据库**

数据库使用 SQLite，初始化入口在 `models/user.go:Init`。运行参数为 `foreign_keys=ON`、`busy_timeout=5000`、`journal_mode=WAL`、`synchronous=NORMAL`。数据库文件默认位于发布目录同级的 `databases/police_style.db`。

主要表结构：

|表|主键|字段|说明|
|---|---|---|---|
|`user`|`username`|`username`, `password`, `salt`|管理员账号。密码为双 SHA\-256 加盐摘要，见 `models/user.go:HashPassword`。|
|`semester`|`semester_name`|`semester_name`, `start_time`, `end_time`|学期范围。日期字段使用 `YYYY-MM-DD` 文本。|
|`students`|`id`|`id`, `stu_name`, `phone_number`|学生基础信息。`phone_number` 可为空，用于定时通知 @ 学生；新增/编辑学生与 Excel 导入（可选「手机号」列）均可维护。|
|`dorm`|`dorm_name`|`dorm_name`, `seq`, `phone_number`|寝室管理。`phone_number` 可为空，用于钉钉 @。|
|`police_style_records_single_subrecords`|`id`|`id`, `submit_date`, `student_name`, `content`, `score`, `include_weekly`|常规扣分记录。`include_weekly` 为数字：`0` 表示不计入区队周扣分，非 `0`（默认 `1`）表示计入。|
|`ownership_single_subrecords`|`(record_id, student_id)`|`record_id`, `student_id`|常规扣分记录与学生的认定关系。|
|`police_style_records_multi_subrecords`|`id`|`id`, `submit_date`, `dorm_name`, `content`, `score`|寝室整体差扣分主记录。|
|`subrecords_for_police_style_records_multi_subrecords`|`id`|`id`, `belongs_to`, `content`|寝室整体差子项。|
|`ownership_multi_subrecords`|`(subrecord_id, student_id)`|`subrecord_id`, `student_id`|寝室整体差子项与负责学生关系。|
|`daily_report_config`|`aes_key`|VPN/内网账号、密码、地址、播报时间、启用状态、自动入库|每日播报配置。密码字段加密存储；`auto_import` 开启后每次日志入库自动导入常规扣分表。|
|`dingtalk_webbook_robots`|`robot_name`|`robot_name`, `dingtalk_webbook_url`, `dingtalk_webbook_password`, `set_status`, `aes_key`|钉钉机器人配置。URL 和加签密钥加密存储。|
|`daily_report_cache`|`id`|`id`, `response_raw`|播报抓取原始响应缓存。|
|`daily_report_log`|`(robot_name, op_time)`|`op_time`, `op_status`, `fetch_content`, `robot_name`, `raw_id`|播报日志，`raw_id` 指向原始响应缓存。|
|`daily_report_auto_run`|`run_key`|`run_key`, `op_time`|自动播报防重复执行记录。|
|`report_events`|`id`|`id`, `scheduled_time`, `status`, `content`, `logs`|定时通知。`status` 为 `0` 未播报／`1` 播报成功／`2` 播报失败；`content` 为播报内容（可含 `@手机号`），`logs` 逐行追加发送结果。|
|`report_event_to_robots`|`(report_id, report_robot_id)`|`report_id`, `report_robot_id`|定时通知与钉钉机器人的多对多关系，`report_id` 外键指向 `report_events(id)`，`report_robot_id` 外键指向 `dingtalk_webbook_robots(robot_name)`。|
|`squad`|`squad_name`|`squad_name`|当前区队名称（单列单行，默认 `24网安二`），用于每日通报表导入时过滤本区队记录。|

```SQL
CREATE TABLE squad (
        squad_name TEXT PRIMARY KEY
);

CREATE TABLE user (
        username TEXT PRIMARY KEY,
        password CHAR(32) NOT NULL,
        salt CHAR(16) NOT NULL
);

CREATE TABLE semester (
        semester_name VARCHAR(255) PRIMARY KEY,
        start_time TEXT,
        end_time TEXT
);

CREATE TABLE students (
        id CHAR(6) PRIMARY KEY,
        stu_name VARCHAR(10) NOT NULL,
        phone_number TEXT
);

CREATE TABLE dorm (
    dorm_name TEXT PRIMARY KEY,
    seq INT, phone_number TEXT
);

CREATE TABLE police_style_records_single_subrecords (
        id CHAR(32) PRIMARY KEY,
        submit_date TEXT,
        student_name VARCHAR(255),
        content TEXT,
        score REAL DEFAULT 0.0,
        include_weekly INTEGER NOT NULL DEFAULT 1
);
CREATE TABLE ownership_single_subrecords (
        record_id CHAR(32),
        student_id CHAR(6),
        PRIMARY KEY (record_id, student_id),
        FOREIGN KEY (student_id) REFERENCES students(id),
        FOREIGN KEY (record_id) REFERENCES police_style_records_single_subrecords(id)
);

CREATE TABLE police_style_records_multi_subrecords (
    id CHAR(32) PRIMARY KEY,
    submit_date TEXT,
    dorm_name VARCHAR(255),
    content TEXT,
    score REAL DEFAULT 0.0
);

CREATE TABLE subrecords_for_police_style_records_multi_subrecords (
    id CHAR(32) PRIMARY KEY, 
    belongs_to CHAR(32), 
    content TEXT, 
    FOREIGN KEY (belongs_to) REFERENCES police_style_records_multi_subrecords(id)
);

CREATE TABLE ownership_multi_subrecords (
    subrecord_id CHAR(32), 
    student_id CHAR(6), 
    PRIMARY KEY (subrecord_id, student_id), 
    FOREIGN KEY (student_id) REFERENCES students(id), 
    FOREIGN KEY (subrecord_id) REFERENCES subrecords_for_police_style_records_multi_subrecords(id)
);

CREATE TABLE daily_report_config(
    aes_key BLOB PRIMARY KEY, 
    vpn_login_url TEXT, 
    username_vpn TEXT, 
    password_vpn BLOB, 
    vpn_police_style_server_url TEXT,
    username_police_style_server TEXT,
    password_police_style_server BLOB,
    fetch_time_everyday TEXT,
    set_status INT,
    auto_import INT DEFAULT 0
);

CREATE TABLE dingtalk_webbook_robots(
    robot_name TEXT PRIMARY KEY, 
    dingtalk_webbook_url BLOB, 
    dingtalk_webbook_password BLOB, 
    set_status INT, aes_key BLOB, 
    FOREIGN KEY(aes_key) REFERENCES daily_report_config(aes_key)
);

CREATE TABLE daily_report_cache(
    id TEXT PRIMARY KEY, 
    response_raw TEXT
);

CREATE TABLE daily_report_auto_run(
    run_key TEXT PRIMARY KEY, 
    op_time TEXT
);

CREATE TABLE daily_report_log(
    op_time TEXT,
    op_status TEXT,
    fetch_content TEXT,
    robot_name TEXT,
    raw_id TEXT,
    PRIMARY KEY(robot_name,op_time),
    FOREIGN KEY(robot_name) REFERENCES dingtalk_webbook_robots(robot_name),
    FOREIGN KEY(raw_id) REFERENCES daily_report_cache(id)
);

CREATE TABLE report_events (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    scheduled_time TEXT,
    status INT NOT NULL DEFAULT 0,
    content TEXT,
    logs TEXT
);

CREATE TABLE report_event_to_robots (
    report_id INTEGER,
    report_robot_id TEXT,
    PRIMARY KEY (report_id, report_robot_id),
    FOREIGN KEY (report_id) REFERENCES report_events(id) ON DELETE CASCADE,
    FOREIGN KEY (report_robot_id) REFERENCES dingtalk_webbook_robots(robot_name)
);
```

E\-R图

![E-R图](https://yhypzc.github.io/posts/%E7%BA%AA%E6%A3%80%E5%B7%A5%E4%BD%9C%E5%8F%B0(PoliceStyleWorkspace)%E6%8A%80%E6%9C%AF%E6%96%87%E6%A1%A3/%E5%9B%BE%E7%89%87%E5%92%8C%E9%99%84%E4%BB%B6/ER.png)



## **构建**

Web 前端要求 Node\.js 20\.19 或更高版本。构建前端：

```PowerShell
cd web
npm install
npm run build
cd ..
```

构建 Go 服务：

```PowerShell
go mod tidy
go build -ldflags="-H windowsgui" -o PoliceStyleWorkspace/bin/police-style-workspace-server.exe .
```

GUI 使用 MinGW 构建 EUI\-NEO，目标为 Windows GUI 子系统：

```PowerShell
cmake -S gui -B gui/build-eui -G "MinGW Makefiles" -DCMAKE_C_COMPILER=gcc -DCMAKE_CXX_COMPILER=g++ -DCMAKE_BUILD_TYPE=Release
cmake --build gui/build-eui --parallel 4
Copy-Item gui/build-eui/police-style-workspace-gui.exe PoliceStyleWorkspace/bin/
```

## **运行**

```PowerShell
.\PoliceStyleWorkspace\bin\police-style-workspace-server.exe -port 3456
.\PoliceStyleWorkspace\bin\police-style-workspace-gui.exe
```

服务器首次启动会输出 `[系统] 初始密码: <明文密码>`。GUI 会自动搜索本地服务器；未运行时可使用“启动服务器”按钮。发布产物位于 `PoliceStyleWorkspace/`，运行数据位于其 `bin/` 同级目录：

- `databases/police_style.db`

- `config/`

- `log/server.log`

- `scripts/server-watchdog.vbs`

GUI 首次启动会生成 `scripts/server-watchdog.vbs` 并注册系统任务。任务每分钟确保无窗口 VBS 监控器存在；VBS 发现服务端消失后等待 1 分钟，再次确认仍未运行才启动。GUI 通过本机命名管道实时接收服务器终端流，支持滚动、框选和 `Ctrl+C` 复制。

## **上游源码**

参考第三方源码保存在 `third_party/`：

- `art-design-pro`：Web 工程导入其样式入口和登录页样式。

https://github\.com/Daymychen/art\-design\-pro

- `EUI-NEO`：GUI 通过 CMake `add_subdirectory` 静态链接。

https://github\.com/sudoevolve/EUI\-NEO

- `Watch-On-Windows`：`gui/src/WatchCapture.cpp` 基于其管道捕获实现改造。

https://github\.com/zmyme/Watch\-On\-Windows

各上游许可证保留在对应目录中。



