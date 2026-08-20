<script setup lang="ts">
import { computed, defineComponent, h, onBeforeUnmount, onMounted, reactive, ref, watch } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { api, clearCSRFToken, csrfHeaders, setCSRFToken } from './api'
import EmojiText from '../../third_party/art-design-pro/src/utils/ui/emojo'

const page = computed(() => location.pathname.includes('daily-report') ? 'daily-report' : location.pathname.includes('change-password') ? 'password' : location.pathname.includes('daily-management') ? 'daily-management' : location.pathname.includes('dorms') ? 'dorms' : location.pathname.includes('students') ? 'students' : location.pathname.includes('multi-deductions') ? 'multi-deductions' : location.pathname.includes('deductions') ? 'deductions' : location.pathname.includes('semester') ? 'semester' : location.pathname.includes('workspace') ? 'workspace' : 'login')
const busy = ref(false)
const authReady = ref(false)
const login = reactive({ username: 'admin', password: '' })
const password = reactive({ old_password: '', new_password: '', confirm: '' })
const SiteFooter = defineComponent({
  setup: () => () => h('footer', { class: 'site-footer' }, [
    '作者 ', h('a', { href: 'https://yhypzc.github.io/', target: '_blank' }, 'yhypzc'),
    ' · ', h('a', { href: 'https://github.com/yhypzc/PoliceStyleWorkspace', target: '_blank' }, '项目仓库')
  ])
})

// ── Semester state & actions ──
interface Semester { semester_name: string; start_time: string; end_time: string }
const semesters = ref<Semester[]>([])
const activeSemester = ref('')
const editing = ref(false)
const newSemester = reactive({ semester_name: '', start_time: '', end_time: '' })
const dailyManagementSemester = ref<Semester | null>(null)
type DailyWeek = { index: number; start: string; end: string; dates: string[] }
type DailyStudentRow = { id: string; name: string; scores: Record<string, number> }
type DailySummaryRow = { id: string; name: string; scores: Record<string, number>; total: number }
const dailyWeeks = ref<DailyWeek[]>([])
const selectedDailyWeek = ref<number | null>(null)
const dailyWeekData = ref<{ week: DailyWeek; rows: DailyStudentRow[] }>({ week: { index: 0, start: '', end: '', dates: [] }, rows: [] })
const dailyManagementBusy = ref(false)
const reportConfig = reactive({ vpn_login_url:'', username_vpn:'', password_vpn:'', vpn_police_style_server_url:'', username_police_style_server:'', password_police_style_server:'', fetch_time_everyday:'08:00', set_status:1 })
const reportRobots = ref<any[]>([])
const reportLogs = ref<any[]>([])
const dailyReportBusy = ref(false)
const reportConfigVisible = ref(false)
const reportDateVisible = ref(false)
const selectedReportDate = ref('')
const robotVisible = ref(false)
const robotForm = reactive({ robot_name:'', dingtalk_webbook_url:'', dingtalk_webbook_password:'', set_status:1 })
const editingRobot = ref<any>(null)
async function loadDailyReport(){ const c=await api<any>('/api/daily-report/config'); if(c.config) Object.assign(reportConfig,c.config); const r=await api<any>('/api/daily-report/robots'); reportRobots.value=r.robots||[]; const l=await api<any>('/api/daily-report/logs'); reportLogs.value=l.logs||[] }
async function saveDailyReport(){ await api('/api/daily-report/config',{method:'PUT',body:JSON.stringify(reportConfig)}); ElMessage.success('播报配置已保存') }
async function exportReportLog(row:any){
  const query = new URLSearchParams({ robot_name: String(row.robot_name || ''), op_time: String(row.op_time || ''), raw_id: String(row.raw_id || '') })
  try {
    const response = await fetch(`/api/daily-report/logs/export?${query.toString()}`, { credentials: 'same-origin' })
    if (!response.ok) {
      const text = await response.text()
      let message = text
      try { message = JSON.parse(text).error || text } catch {}
      throw new Error(message || `导出失败 (${response.status})`)
    }
    const blob = await response.blob()
    const url = URL.createObjectURL(blob)
    const link = document.createElement('a')
    link.href = url
    link.download = '警务化扣分记录.xlsx'
    link.click()
    URL.revokeObjectURL(url)
  } catch (error:any) {
    ElMessage.error(error.message || '导出失败')
  }
}
async function deleteReportLog(row:any){
  try {
    await ElMessageBox.confirm(`确定删除 ${row.op_time} 的播报日志吗？`, '确认删除', { type: 'warning', confirmButtonText: '删除', cancelButtonText: '取消' })
  } catch { return }
  dailyReportBusy.value = true
  try {
    await api('/api/daily-report/logs', {
      method: 'DELETE',
      body: JSON.stringify({ robot_name: row.robot_name, op_time: row.op_time })
    })
    reportLogs.value = reportLogs.value.filter((item:any) => item.log_id !== row.log_id)
    ElMessage.success('播报日志已删除')
  } catch (error:any) {
    ElMessage.error(error.message)
  } finally {
    dailyReportBusy.value = false
  }
}
async function runDailyReport(){
  if (dailyReportBusy.value) return
  dailyReportBusy.value = true
  try {
    await api('/api/daily-report/run',{method:'POST'})
    const l=await api<any>('/api/daily-report/logs')
    reportLogs.value=l.logs||[]
    ElMessage.success('播报完成')
  } catch(error:any) {
    ElMessage.error(error.message)
  } finally {
    dailyReportBusy.value = false
  }
}
async function runDailyReportForDate(){
  if (dailyReportBusy.value) return
  if (!selectedReportDate.value) {
    ElMessage.error('请选择播报日期')
    return
  }
  dailyReportBusy.value = true
  try {
    await api('/api/daily-report/run',{method:'POST',body:JSON.stringify({ date: selectedReportDate.value })})
    const l=await api<any>('/api/daily-report/logs')
    reportLogs.value=l.logs||[]
    reportDateVisible.value = false
    ElMessage.success('播报完成')
  } catch(error:any) {
    ElMessage.error(error.message)
  } finally {
    dailyReportBusy.value = false
  }
}
async function saveRobot(){ await api('/api/daily-report/robots',{method:'POST',body:JSON.stringify(robotForm)}); reportRobots.value=(await api<any>('/api/daily-report/robots')).robots||[]; editingRobot.value=null; robotForm.robot_name='';robotForm.dingtalk_webbook_url='';robotForm.dingtalk_webbook_password='';robotForm.set_status=1;ElMessage.success('机器人已保存') }
function maskRobotValue(value:string, isSecret = false) { if (!value) return '未设置'; if (isSecret) return '********'; if (value.length <= 12) return value.slice(0, 4) + '****' + value.slice(-2); return value.slice(0, 8) + '********' + value.slice(-4) }
function editRobot(row:any){ editingRobot.value={ ...row } }
async function saveEditingRobot(){ if (!editingRobot.value) return; try { await api('/api/daily-report/robots',{method:'POST',body:JSON.stringify({ ...editingRobot.value, update: true })}); reportRobots.value=(await api<any>('/api/daily-report/robots')).robots||[]; editingRobot.value=null; ElMessage.success('机器人配置已保存') } catch (error:any) { ElMessage.error(error.message) } }
async function toggleRobot(row:any){
  const nextStatus = row.set_status ? 1 : 0
  try {
    await api('/api/daily-report/robots',{
      method:'POST',
      body:JSON.stringify({ ...row, set_status: nextStatus, update: true })
    })
    row.set_status = nextStatus
    ElMessage.success(nextStatus ? '机器人已启用' : '机器人已禁用')
  } catch (error:any) {
    row.set_status = nextStatus ? 0 : 1
    ElMessage.error(error.message)
  }
}
async function deleteRobot(row:any){ try { await api('/api/daily-report/robots',{method:'DELETE',body:JSON.stringify({robot_name: row.robot_name})}); reportRobots.value=reportRobots.value.filter(r=>r.robot_name!==row.robot_name); ElMessage.success('机器人已删除') } catch (error:any) { ElMessage.error(error.message) } }
const dailySummaryVisible = ref(false)
const showWeekRecords = ref(false)
const dailySummaryRows = ref<DailySummaryRow[]>([])
const dailyExportOrderVisible = ref(false)
const dailyExportOrderRows = ref<DailyStudentRow[]>([])
const dailyExportDraggedID = ref('')
type WorkspaceStats = { single_deduction_count: number; multi_deduction_count: number; multi_without_subrecords_count: number; total_score: number; unassigned_count: number; out_of_semester_count: number; duty_dorm_name: string }
const workspaceStats = ref<WorkspaceStats>({ single_deduction_count: 0, multi_deduction_count: 0, multi_without_subrecords_count: 0, total_score: 0, unassigned_count: 0, out_of_semester_count: 0, duty_dorm_name: '' })
const workspaceBusy = ref(false)
type WorkspaceRecords = { single: Deduction[]; multi: MultiDeduction[] }
const workspaceRecordsVisible = ref(false)
const workspaceRecordsTitle = ref('')
const workspaceRecords = ref<WorkspaceRecords>({ single: [], multi: [] })
const currentTime = ref(new Date())
let clockTimer: ReturnType<typeof setInterval> | undefined
let clockSyncTimer: ReturnType<typeof setInterval> | undefined
let serverClockOffset = 0
const currentTimeText = computed(() => currentTime.value.toLocaleString('zh-CN', { hour12: false }))

// ── Dorm state & actions ──
type Dorm = { dorm_name: string; seq: number; phone_number: string }
const dorms = ref<Dorm[]>([])
const dormBusy = ref(false)
const dormDialogVisible = ref(false)
const editingDorm = ref<Dorm | null>(null)
const dormName = ref('')
const dormPhoneNumber = ref('')
const dormOrderVisible = ref(false)
const sortableDorms = ref<Dorm[]>([])
const draggedDormName = ref('')

// ── Student state ──
const students = ref<{ id: string; stu_name: string }[]>([])
const studentBusy = ref(false)
const editingStudent = ref<{ id: string; stu_name: string } | null>(null)
const studentEditForm = reactive({ id: '', stu_name: '' })
const studentBatchDeleteVisible = ref(false)
const studentBatchDeleteSelection = ref<string[]>([])
const studentBatchDeleteFilters = reactive({ universalEnabled: false, universal: '', fieldEnabled: false, fields: [{ field: 'id', value: '' }] })

// ── Deduction state ──
type Deduction = { id: string; submit_date: string; student_name: string; recognized_students: string; recognized_student_ids: string[]; content: string; score: string }
const deductions = ref<Deduction[]>([])
const deductionSearch = ref('')
const deductionBusy = ref(false)
const editingDeduction = ref<{ id: string; submit_date: string; student_name: string; content: string; score: string } | null>(null)
const editingRecognition = ref<Deduction | null>(null)
const recognizedStudentIDs = ref<string[]>([])
const deductionEditForm = reactive({ id: '', submit_date: '', student_name: '', content: '', score: '' })
const deductionImportResult = ref<{ imported: number; errors?: string[] } | null>(null)
const importResult = ref<{ imported: number; errors?: string[] } | null>(null)
type MultiDeduction = { id: string; submit_date: string; dorm_name: string; content: string; score: number }
type MultiSubrecord = { id: string; belongs_to: string; content: string; student_ids: string[]; student_names: string }
const multiDeductions = ref<MultiDeduction[]>([])
const multiDeductionBusy = ref(false)
const multiDeductionSearch = ref('')
const selectedMultiDeductionIDs = ref<string[]>([])
const multiBatchDeleteVisible = ref(false)
const multiBatchDeleteSelection = ref<string[]>([])
const multiBatchDeleteFilters = reactive({ universalEnabled: false, universal: '', fieldEnabled: false, fields: [{ field: 'id', value: '' }], dateEnabled: false, dateRange: [] as string[] })
const editingMultiDeduction = ref<MultiDeduction | null>(null)
const multiDeductionForm = reactive({ submit_date: '', dorm_name: '', content: '', score: 0 })
const managingSubrecords = ref<MultiDeduction | null>(null)
const multiSubrecords = ref<MultiSubrecord[]>([])
const multiSubrecordForm = reactive({ id: '', content: '', student_ids: [] as string[] })
const batchDeleteVisible = ref(false)
const batchDeleteSelection = ref<string[]>([])
const batchDeleteFilters = reactive({
  universalEnabled: false,
  universal: '',
  fieldEnabled: false,
  fields: [{ field: 'id', value: '' }],
  dateEnabled: false,
  dateRange: [] as string[]
})
const filteredDeductions = computed(() => {
  const keyword = deductionSearch.value.trim().toLocaleLowerCase()
  if (!keyword) return deductions.value
  return deductions.value.filter((record) => [
    record.id,
    record.student_name,
    record.recognized_students,
    record.submit_date,
    record.content,
    record.score
  ].some((value) => String(value ?? '').toLocaleLowerCase().includes(keyword)))
})
const studentBatchDeleteCandidates = computed(() => {
  const filters = studentBatchDeleteFilters
  if (!filters.universalEnabled && !filters.fieldEnabled) return students.value
  return students.value.filter((student) => {
    if (filters.universalEnabled) {
      const keyword = filters.universal.trim().toLocaleLowerCase()
      if (keyword && ![student.id, student.stu_name].some((value) => value.toLocaleLowerCase().includes(keyword))) return false
    }
    if (filters.fieldEnabled && !filters.fields.every((filter) => {
      const keyword = filter.value.trim().toLocaleLowerCase()
      return !keyword || String(student[filter.field as keyof typeof student] ?? '').toLocaleLowerCase().includes(keyword)
    })) return false
    return true
  })
})
const batchDeleteCandidates = computed(() => {
  const activeFilters = batchDeleteFilters.universalEnabled || batchDeleteFilters.fieldEnabled || batchDeleteFilters.dateEnabled
  if (!activeFilters) return deductions.value
  return deductions.value.filter((record) => {
    if (batchDeleteFilters.universalEnabled) {
      const keyword = batchDeleteFilters.universal.trim().toLocaleLowerCase()
      if (keyword && ![record.id, record.student_name, record.recognized_students, record.submit_date, record.content, record.score]
        .some((value) => String(value ?? '').toLocaleLowerCase().includes(keyword))) return false
    }
    if (batchDeleteFilters.fieldEnabled) {
      const fieldMatches = batchDeleteFilters.fields.every((filter) => {
        const keyword = filter.value.trim().toLocaleLowerCase()
        const value = String(record[filter.field as keyof Deduction] ?? '').toLocaleLowerCase()
        return !keyword || value.includes(keyword)
      })
      if (!fieldMatches) return false
    }
    if (batchDeleteFilters.dateEnabled && batchDeleteFilters.dateRange.length === 2) {
      const [start, end] = batchDeleteFilters.dateRange
      if (record.submit_date < start || record.submit_date > `${end} 23:59:59`) return false
    }
    return true
  })
})
const filteredMultiDeductions = computed(() => {
  const keyword = multiDeductionSearch.value.trim().toLocaleLowerCase()
  if (!keyword) return multiDeductions.value
  return multiDeductions.value.filter((record) => [record.id, record.submit_date, record.dorm_name, record.content, record.score].some((value) => String(value).toLocaleLowerCase().includes(keyword)))
})
const multiBatchDeleteCandidates = computed(() => {
  const filters = multiBatchDeleteFilters
  if (!filters.universalEnabled && !filters.fieldEnabled && !filters.dateEnabled) return multiDeductions.value
  return multiDeductions.value.filter((record) => {
    if (filters.universalEnabled) { const key = filters.universal.trim().toLocaleLowerCase(); if (key && ![record.id, record.submit_date, record.dorm_name, record.content, record.score].some((value) => String(value).toLocaleLowerCase().includes(key))) return false }
    if (filters.fieldEnabled && !filters.fields.every((filter) => { const key = filter.value.trim().toLocaleLowerCase(); return !key || String(record[filter.field as keyof MultiDeduction] ?? '').toLocaleLowerCase().includes(key) })) return false
    if (filters.dateEnabled && filters.dateRange.length === 2) { const [start, end] = filters.dateRange; if (record.submit_date < start || record.submit_date > `${end} 23:59:59`) return false }
    return true
  })
})

onMounted(async () => {
  if (page.value === 'login') { authReady.value = true; return }
  try { await api('/api/check-auth') } catch { /* api() performs the redirect. */ return }
  authReady.value = true
  clockTimer = setInterval(() => { currentTime.value = new Date(Date.now() + serverClockOffset) }, 1000)
  if (await syncServerClock()) clockSyncTimer = setInterval(() => { void syncServerClock() }, 60_000)
  if (page.value === 'workspace') await loadWorkspaceStats()
  if (page.value === 'semester') await loadSemesters()
  if (page.value === 'daily-management') await loadSemesters()
  if (page.value === 'daily-report') await loadDailyReport()
  if (page.value === 'dorms') await loadDorms()
  if (page.value === 'students') await loadStudents()
  if (page.value === 'deductions') await loadDeductions()
  if (page.value === 'multi-deductions') await loadMultiDeductions()
})

function refreshStudentsWhenPageShown() {
  if (page.value === 'students' && authReady.value) void loadStudents()
}

onMounted(() => window.addEventListener('pageshow', refreshStudentsWhenPageShown))
onBeforeUnmount(() => {
  window.removeEventListener('pageshow', refreshStudentsWhenPageShown)
  if (clockTimer) clearInterval(clockTimer)
  if (clockSyncTimer) clearInterval(clockSyncTimer)
})

async function syncServerClock(): Promise<boolean> {
  try {
    const response = await api<{ unix_milliseconds: number }>('/api/clock', { cache: 'no-store' })
    serverClockOffset = response.unix_milliseconds - Date.now()
    currentTime.value = new Date(Date.now() + serverClockOffset)
    return true
  } catch {
    if (clockSyncTimer) clearInterval(clockSyncTimer)
    clockSyncTimer = undefined
    return false
  }
}

watch(page, (newPage) => {
  if (newPage === 'workspace') loadWorkspaceStats()
  if (newPage === 'semester') loadSemesters()
  if (newPage === 'daily-management') loadSemesters()
  if (newPage === 'dorms') loadDorms()
  if (newPage === 'students') loadStudents()
  if (newPage === 'deductions') loadDeductions()
  if (newPage === 'multi-deductions') loadMultiDeductions()
})

async function loadSemesters() {
  const res = await api<{ semesters: Semester[] }>('/api/semesters')
  semesters.value = res.semesters
  if (semesters.value.length > 0 && !activeSemester.value) {
    activeSemester.value = semesters.value[0].semester_name
  }
}
const currentSemester = computed(() => {
  if (!activeSemester.value) return null
  return semesters.value.find(s => s.semester_name === activeSemester.value) || null
})
async function createSemester() {
  if (!newSemester.semester_name || !newSemester.start_time || !newSemester.end_time) {
    return ElMessage.warning('请填写完整的学期信息')
  }
  busy.value = true
  try {
    const res = await api<{ semester: Semester }>('/api/semesters', { method: 'POST', body: JSON.stringify(newSemester) })
    ElMessage.success('学期创建成功')
    newSemester.semester_name = ''
    newSemester.start_time = ''
    newSemester.end_time = ''
    await loadSemesters()
    activeSemester.value = res.semester.semester_name
    editing.value = false
  } catch (error: any) { ElMessage.error(error.message) }
  finally { busy.value = false }
}
async function saveSemester() {
  const s = currentSemester.value
  if (!s) return
  busy.value = true
  try {
    await api(`/api/semesters/${encodeURIComponent(s.semester_name)}`, { method: 'PUT', body: JSON.stringify(s) })
    ElMessage.success('学期已保存')
    await loadSemesters()
    editing.value = false
  } catch (error: any) { ElMessage.error(error.message) }
  finally { busy.value = false }
}
async function deleteSemester() {
  const s = currentSemester.value
  if (!s) return
  try {
    await ElMessageBox.confirm(
      `确定要删除学期 "${s.semester_name}" 吗？此操作不可撤销。`,
      '确认删除',
      { type: 'warning', confirmButtonText: '删除', cancelButtonText: '取消' }
    )
  } catch { return }
  busy.value = true
  try {
    await api(`/api/semesters/${encodeURIComponent(s.semester_name)}`, { method: 'DELETE' })
    ElMessage.success('学期已删除')
    await loadSemesters()
    if (semesters.value.length > 0) activeSemester.value = semesters.value[0].semester_name
    else { activeSemester.value = ''; editing.value = false }
  } catch (error: any) { ElMessage.error(error.message) }
  finally { busy.value = false }
}
function startEdit() { editing.value = true }
function cancelEdit() { editing.value = false; loadSemesters() }
function onStartChange(val: string) {
  if (!val) return
  const d = new Date(val)
  if (d.getDay() !== 6) { ElMessage.warning('起始日期必须是周六'); newSemester.start_time = ''; return }
  const end = new Date(d)
  end.setDate(end.getDate() + 6)
  // end date now manually selected
}
function onEditStartChange(val: string) {
  const s = currentSemester.value
  if (!s || !val) return
  const d = new Date(val)
  if (d.getDay() !== 6) { ElMessage.warning('起始日期必须是周六'); s.start_time = ''; return }
  const end = new Date(d); end.setDate(end.getDate() + 6)
  // end date now manually selected
}

async function submitLogin() {
  busy.value = true
  try {
    const timestamp = Date.now()
    const result = await api<{ csrf_token?: string }>('/api/login', {
      method: 'POST',
      headers: { 'X-Login-Timestamp': String(timestamp) },
      body: JSON.stringify({ ...login, timestamp })
    })
    setCSRFToken(result.csrf_token || '')
    location.assign('/workspace')
  } catch (error) { ElMessage.error(`${EmojiText['400']} ${(error as Error).message}`) }
  finally { busy.value = false }
}
async function submitPassword() {
  if (password.new_password !== password.confirm) return ElMessage.error('两次输入的新密码不一致')
  busy.value = true
  try {
    await api('/api/change-password', { method: 'POST', body: JSON.stringify(password) })
    ElMessage.success(`${EmojiText['200']} 密码已修改，请重新登录`)
    setTimeout(() => location.replace('/login'), 600)
  } catch (error) { ElMessage.error(`${EmojiText['400']} ${(error as Error).message}`) }
  finally { busy.value = false }
}

async function logout() {
  try { await api('/api/logout', { method: 'POST' }) } catch { /* ignore */ }
  clearCSRFToken()
  location.replace('/login')
}
async function openDailyManagementSemester(semester: Semester) {
  dailyManagementSemester.value = semester
  const start = new Date(`${semester.start_time}T00:00:00`)
  const end = new Date(`${semester.end_time}T00:00:00`)
  const weeks: DailyWeek[] = []
  for (let index = 0, cursor = new Date(start); cursor <= end; index++, cursor.setDate(cursor.getDate() + 7)) {
    const weekEnd = new Date(cursor); weekEnd.setDate(weekEnd.getDate() + 6)
    if (weekEnd > end) weekEnd.setTime(end.getTime())
    const dates: string[] = []
    for (const day = new Date(cursor); day <= weekEnd; day.setDate(day.getDate() + 1)) dates.push(formatLocalDate(day))
    weeks.push({ index, start: formatLocalDate(cursor), end: formatLocalDate(weekEnd), dates })
  }
  dailyWeeks.value = weeks
  if (weeks.length) await selectDailyWeek(weeks[0].index)
}
function closeDailyManagementSemester() { dailyManagementSemester.value = null; dailyWeeks.value = []; selectedDailyWeek.value = null; dailyWeekData.value = { week: { index: 0, start: '', end: '', dates: [] }, rows: [] } }
async function openDailySummary() {
  if (!dailyManagementSemester.value) return
  dailyManagementBusy.value = true
  try {
    const result = await api<{ weeks: DailyWeek[]; rows: DailySummaryRow[] }>(`/api/daily-management/${encodeURIComponent(dailyManagementSemester.value.semester_name)}/summary`, { cache: 'no-store' })
    dailyWeeks.value = result.weeks
    dailySummaryRows.value = result.rows
    dailySummaryVisible.value = true
  } catch (error: any) { ElMessage.error(error.message) }
  finally { dailyManagementBusy.value = false }
}
async function selectDailyWeek(index: number) {
  if (!dailyManagementSemester.value) return
  dailySummaryVisible.value = false
  clearWeekBatchSelection()
  dailyManagementBusy.value = true
  try {
    dailyWeekData.value = (await api<{ week: DailyWeek; rows: DailyStudentRow[] }>(`/api/daily-management/${encodeURIComponent(dailyManagementSemester.value.semester_name)}/weeks/${index}`, { cache: 'no-store' }))
    selectedDailyWeek.value = index
    punishmentList.value = []
    loadPunishmentList()
    if (showWeekRecords.value) loadWeekRecords()
  } catch (error: any) { ElMessage.error(error.message) }
  finally { dailyManagementBusy.value = false }
}
function formatDailyDate(value: string) { return value.slice(5).replace('-', '/') }
function closeDailySummary() { dailySummaryVisible.value = false }

type WeekRecords = { single: { id: string; date: string; content: string; score: number; student_ids: string[]; student_names: string }[]; multi: { id: string; date: string; content: string; score: number; subs: { sub_id: string; content: string; student_ids: string[]; student_names: string }[] }[] }
const weekRecords = ref<WeekRecords>({ single: [], multi: [] })
const weekRecordsBusy = ref(false)
const weekBatchSingleIDs = ref<string[]>([])
const weekBatchMultiIDs = ref<string[]>([])
const weekBatchExportBusy = ref(false)

// ── Appeal dialog state ──
const appealDialogVisible = ref(false)
const appealRecord = ref<any>(null)
const appealIsSchool = ref(false)
const appealType = ref("single")
const appealGrade = ref('')
const appealClass = ref('')
const appealText = ref('')
const appealDdPhotos = ref<string[]>([])
const appealAppealPhotos = ref<string[]>([])
// Appeal data is keyed by the record ID alone on the server (evidence folders
// and the JSON store use the same ID), so the key stays stable even when the
// record's student names or recognition are edited later.
const appealKey = computed(() => appealRecord.value?.id || '')
const appealSaving = ref(false)
const appealExporting = ref(false)
async function loadWeekRecords() {
  if (!dailyManagementSemester.value || selectedDailyWeek.value === null) return
  weekRecordsBusy.value = true
  try {
    const res = await api<WeekRecords & { ok: boolean }>(`/api/daily-management/${encodeURIComponent(dailyManagementSemester.value.semester_name)}/weeks/${selectedDailyWeek.value}/records`)
    if (res.ok) weekRecords.value = { single: res.single || [], multi: res.multi || [] }
  } catch (error: any) { ElMessage.error(error.message) }
  finally { weekRecordsBusy.value = false }
}
function clearWeekBatchSelection() {
  weekBatchSingleIDs.value = []
  weekBatchMultiIDs.value = []
}
function toggleWeekSingle(id: string, checked: boolean) {
  weekBatchSingleIDs.value = checked
    ? Array.from(new Set([...weekBatchSingleIDs.value, id]))
    : weekBatchSingleIDs.value.filter(item => item !== id)
}
function toggleWeekMulti(id: string, checked: boolean) {
  weekBatchMultiIDs.value = checked
    ? Array.from(new Set([...weekBatchMultiIDs.value, id]))
    : weekBatchMultiIDs.value.filter(item => item !== id)
}
function toggleAllWeekSingle(checked: boolean) {
  weekBatchSingleIDs.value = checked ? weekRecords.value.single.map(row => row.id) : []
}
function toggleAllWeekMulti(checked: boolean) {
  weekBatchMultiIDs.value = checked ? weekRecords.value.multi.map(row => row.id) : []
}
async function batchExportWeekAppeals() {
  const single = [...weekBatchSingleIDs.value]
  const multi = [...weekBatchMultiIDs.value]
  if (single.length === 0 && multi.length === 0) {
    ElMessage.warning('请先勾选需要导出的扣分记录')
    return
  }
  weekBatchExportBusy.value = true
  try {
    const response = await fetch('/api/appeal/batch-export', {
      method: 'POST',
      credentials: 'same-origin',
      headers: { 'Content-Type': 'application/json', ...csrfHeaders() },
      body: JSON.stringify({ single, multi })
    })
    if (!response.ok) {
      const text = await response.text()
      let message = text
      try { message = JSON.parse(text).error || text } catch {}
      throw new Error(message || `批量导出失败 (${response.status})`)
    }
    const blob = await response.blob()
    const objectURL = URL.createObjectURL(blob)
    const link = document.createElement('a')
    link.href = objectURL
    link.download = '申诉汇总.zip'
    link.click()
    URL.revokeObjectURL(objectURL)
    ElMessage.success('批量导出已完成')
  } catch (error: any) {
    ElMessage.error(error.message || '批量导出失败')
  } finally {
    weekBatchExportBusy.value = false
  }
}
async function openAppeal(row: any, type: string) {
  // 管理页行使用 submit_date/student_name，日常管理行使用 date/student_names，
  // 统一归一化供申诉弹窗使用
  appealRecord.value = {
    ...row,
    date: row.date || row.submit_date || '',
    student_names: row.student_names || row.student_name || row.recognized_students || ''
  }
  appealIsSchool.value = (row.id || '').startsWith('xd_')
  appealSaving.value = true
  try {
    const res = await api<any>(`/api/appeal/config?key=${encodeURIComponent(appealKey.value)}`)
    if (res.config && Object.keys(res.config).length > 0) {
      appealGrade.value = res.config.grade || ''
      appealClass.value = res.config.class || ''
      appealText.value = res.config.text_content || ''
      appealDdPhotos.value = res.config.dd_photos || []
      appealAppealPhotos.value = res.config.appeal_photos || []
    } else {
      appealGrade.value = ''
      appealClass.value = ''
      appealText.value = ''
      appealDdPhotos.value = []
      appealAppealPhotos.value = []
    }
  } catch { appealGrade.value = ''; appealClass.value = ''; appealText.value = '' }
  finally { appealSaving.value = false }
  appealDialogVisible.value = true
}
async function saveAppealConfig() {
  if (!appealRecord.value) return
  appealSaving.value = true
  try {
    await api('/api/appeal/config', { method: 'POST', body: JSON.stringify({
      key: appealKey.value, grade: appealGrade.value, class: appealClass.value,
      text_content: appealText.value, dd_photos: appealDdPhotos.value, appeal_photos: appealAppealPhotos.value
    })})
    ElMessage.success('已保存')
  } catch (e: any) { ElMessage.error(e.message) }
  finally { appealSaving.value = false }
}
async function uploadAppealPhoto(type: string) {
  const input = document.createElement('input'); input.type = 'file'; input.accept = '.jpg,.jpeg,.png,.webp'
  input.onchange = async () => {
    if (!input.files || !input.files[0]) return
    const form = new FormData(); form.append('photo', input.files[0])
    try {
      const res = await fetch(`/api/appeal/upload-photo?key=${encodeURIComponent(appealKey.value)}&type=${type}`, { method: 'POST', body: form, credentials: 'same-origin', headers: csrfHeaders() })
      const data = await res.json()
      if (!data.ok) { ElMessage.error(data.error); return }
      ElMessage.success('上传成功')
      if (type === 'dd') appealDdPhotos.value = [...appealDdPhotos.value, data.filename]
      else appealAppealPhotos.value = [...appealAppealPhotos.value, data.filename]
    } catch (e: any) { ElMessage.error('上传失败') }
  }
  input.click()
}
async function deleteAppealPhoto(filename: string, type: string) {
  try {
    await api('/api/appeal/delete-photo', { method: 'POST', headers: csrfHeaders(), body: JSON.stringify({ key: appealKey.value, filename }) })
    if (type === 'dd') appealDdPhotos.value = appealDdPhotos.value.filter(f => f !== filename)
    else appealAppealPhotos.value = appealAppealPhotos.value.filter(f => f !== filename)
    ElMessage.success('已删除')
  } catch (e: any) { ElMessage.error(e.message) }
}
async function exportAppealZip() {
  if (!appealRecord.value || appealExporting.value) return
  appealExporting.value = true
  try {
    await saveAppealConfig()
    const params = new URLSearchParams({ id: appealRecord.value.id })
    const response = await fetch('/api/appeal/export-zip?' + params.toString(), { credentials: 'same-origin' })
    if (!response.ok) throw new Error('导出失败')
    const blob = await response.blob()
    const cd = response.headers.get('content-disposition') || ''
    const name = cd.match(/filename\*=UTF-8''([^;]+)/)?.[1]
      ? decodeURIComponent(RegExp.$1)
      : (cd.match(/filename="?([^\";]+)"?/)?.[1] || 'appeal.zip')
    const url = URL.createObjectURL(blob)
    const link = document.createElement('a')
    link.href = url
    link.download = name
    link.click()
    URL.revokeObjectURL(url)
  } catch (error: any) {
    ElMessage.error(error?.message || '导出失败')
  } finally {
    appealExporting.value = false
  }
}
function toggleWeekRecords() {
  showWeekRecords.value = !showWeekRecords.value
  if (showWeekRecords.value) loadWeekRecords()
  else clearWeekBatchSelection()
}
async function openDailyWeekExport() {
  if (!dailyManagementSemester.value || selectedDailyWeek.value === null) return
  try {
    const pref = await api<{ order: string[] }>(`/api/daily-management/${encodeURIComponent(dailyManagementSemester.value.semester_name)}/weeks/${selectedDailyWeek.value}/export-preferences`, { cache: 'no-store' })
    const byID = new Map(dailyWeekData.value.rows.map(row => [row.id, row]))
    const ordered = (pref.order || []).map(id => byID.get(id)).filter((row): row is DailyStudentRow => !!row)
    dailyExportOrderRows.value = [...ordered, ...dailyWeekData.value.rows.filter(row => !pref.order?.includes(row.id))]
    dailyExportOrderVisible.value = true
  } catch (error: any) { ElMessage.error(error.message) }
}
function onDailyExportDragStart(id: string) { dailyExportDraggedID.value = id }
function onDailyExportDrop(id: string) {
  const from = dailyExportOrderRows.value.findIndex(row => row.id === dailyExportDraggedID.value); const to = dailyExportOrderRows.value.findIndex(row => row.id === id)
  if (from < 0 || to < 0 || from === to) return
  const [row] = dailyExportOrderRows.value.splice(from, 1); dailyExportOrderRows.value.splice(to, 0, row); dailyExportDraggedID.value = ''
}
async function exportDailyWeek() {
  if (!dailyManagementSemester.value || selectedDailyWeek.value === null) return
  const order = dailyExportOrderRows.value.map(row => row.id)
  try {
    await api(`/api/daily-management/${encodeURIComponent(dailyManagementSemester.value.semester_name)}/weeks/${selectedDailyWeek.value}/export-preferences`, { method: 'POST', body: JSON.stringify({ order }) })
    const response = await fetch(`/api/daily-management/${encodeURIComponent(dailyManagementSemester.value.semester_name)}/weeks/${selectedDailyWeek.value}/export?order=${encodeURIComponent(JSON.stringify(order))}`, { credentials: 'same-origin' })
    if (!response.ok) throw new Error('导出失败')
    const blob = await response.blob(); const url = URL.createObjectURL(blob); const link = document.createElement('a'); link.href = url; link.download = `${dailyManagementSemester.value.semester_name}-第${selectedDailyWeek.value + 1}周扣分.xlsx`; link.click(); URL.revokeObjectURL(url); dailyExportOrderVisible.value = false
  } catch (error: any) { ElMessage.error(error.message) }
}
async function exportDailySummary() { if (!dailyManagementSemester.value) return; const response = await fetch(`/api/daily-management/${encodeURIComponent(dailyManagementSemester.value.semester_name)}/summary/export`, { credentials: 'same-origin' }); if (!response.ok) return ElMessage.error('导出失败'); const blob = await response.blob(); const url = URL.createObjectURL(blob); const link = document.createElement('a'); link.href = url; link.download = `${dailyManagementSemester.value.semester_name}-学期汇总.xlsx`; link.click(); URL.revokeObjectURL(url) }
function formatLocalDate(date: Date) { const month = String(date.getMonth() + 1).padStart(2, '0'); const day = String(date.getDate()).padStart(2, '0'); return `${date.getFullYear()}-${month}-${day}` }
function formatSummaryScore(value: number) {
  if (value === 0) return '0.0'
  // Detect terminating decimal values before falling back to six decimals.
  for (let digits = 0; digits <= 15; digits++) {
    const scale = 10 ** digits
    const rounded = Math.round(value * scale) / scale
    if (Math.abs(value - rounded) < 1e-10) {
      return digits === 0 ? String(rounded) : rounded.toFixed(digits).replace(/0+$/, '').replace(/\.$/, '')
    }
  }
  return value.toFixed(6)
}
function formatDailyCellScore(value: number) { return value === 0 ? '' : formatSummaryScore(value) }
function dailyRowTotal(row: DailyStudentRow) { return Object.values(row.scores).reduce((sum, score) => sum + score, 0) }
function dailyRowClassName({ row }: { row: DailyStudentRow }) { return punishmentIDSet.value.has(row.id) ? 'daily-score-highlight' : '' }
const dailyDisciplineNames = computed(() => punishmentList.value.map((e) => e.student_name))

type PunishmentEntry = { student_id: string; student_name: string; total: number; records: { record_id: string; date: string; content: string; student_count: number; is_multi: boolean; raw_score: number; logic_score: number }[] }
const punishmentList = ref<PunishmentEntry[]>([])
async function loadPunishmentList() {
  if (!dailyManagementSemester.value || selectedDailyWeek.value === null) return
  punishmentList.value = []
  const semester = dailyManagementSemester.value.semester_name
  const week = selectedDailyWeek.value
  try {
    const res = await api<{ entries: PunishmentEntry[] }>(`/api/workspace/punishment-list/${encodeURIComponent(semester)}/weeks/${week}`)
    if (dailyManagementSemester.value?.semester_name === semester && selectedDailyWeek.value === week) {
      punishmentList.value = Array.isArray(res.entries) ? res.entries : []
    }
  } catch { /* keep empty */ }
}
const punishmentIDSet = computed(() => new Set(punishmentList.value.map(e => e.student_id)))
const selectedPunishmentStudent = ref<PunishmentEntry | null>(null)
const punishmentDetailVisible = ref(false)
function openPunishmentDetail(row: PunishmentEntry) { selectedPunishmentStudent.value = row; punishmentDetailVisible.value = true }
function formatDetailDate(d: string) { return d.slice(5) }

// ── Dorm actions ──
async function loadDorms() {
  dormBusy.value = true
  try { dorms.value = (await api<{ dorms: Dorm[] }>('/api/dorms', { cache: 'no-store' })).dorms }
  catch (error: any) { ElMessage.error(error.message) }
  finally { dormBusy.value = false }
}
function openCreateDorm() { editingDorm.value = null; dormName.value = ''; dormPhoneNumber.value = ''; dormDialogVisible.value = true }
function openEditDorm(dorm: Dorm) { editingDorm.value = dorm; dormName.value = dorm.dorm_name; dormPhoneNumber.value = dorm.phone_number || ''; dormDialogVisible.value = true }
async function saveDorm() {
  const name = dormName.value.trim()
  const phoneNumber = dormPhoneNumber.value.trim()
  if (!name) return ElMessage.warning('请输入寝室名称')
  dormBusy.value = true
  try {
    if (editingDorm.value) await api(`/api/dorms/${encodeURIComponent(editingDorm.value.dorm_name)}`, { method: 'PUT', body: JSON.stringify({ dorm_name: name, phone_number: phoneNumber }) })
    else await api('/api/dorms', { method: 'POST', body: JSON.stringify({ dorm_name: name, phone_number: phoneNumber }) })
    dormDialogVisible.value = false
    await loadDorms()
    ElMessage.success(editingDorm.value ? '寝室名称已更新' : '寝室已插入')
  } catch (error: any) { ElMessage.error(error.message) }
  finally { dormBusy.value = false }
}
async function deleteDorm(dorm: Dorm) {
  try { await ElMessageBox.confirm(`确定要删除寝室 "${dorm.dorm_name}" 吗？`, '确认删除', { type: 'warning', confirmButtonText: '删除', cancelButtonText: '取消' }) } catch { return }
  dormBusy.value = true
  try { await api(`/api/dorms/${encodeURIComponent(dorm.dorm_name)}`, { method: 'DELETE' }); await loadDorms(); ElMessage.success('寝室已删除') }
  catch (error: any) { ElMessage.error(error.message) }
  finally { dormBusy.value = false }
}
function openDormOrder() { sortableDorms.value = dorms.value.map((dorm) => ({ ...dorm })); draggedDormName.value = ''; dormOrderVisible.value = true }
function onDormDragStart(name: string) { draggedDormName.value = name }
function onDormDrop(targetName: string) {
  const from = sortableDorms.value.findIndex((dorm) => dorm.dorm_name === draggedDormName.value)
  const to = sortableDorms.value.findIndex((dorm) => dorm.dorm_name === targetName)
  if (from < 0 || to < 0 || from === to) return
  const [moved] = sortableDorms.value.splice(from, 1)
  sortableDorms.value.splice(to, 0, moved)
}
async function saveDormOrder() {
  dormBusy.value = true
  try {
    await api('/api/dorms/reorder', { method: 'PUT', body: JSON.stringify({ names: sortableDorms.value.map((dorm) => dorm.dorm_name) }) })
    dormOrderVisible.value = false
    await loadDorms()
    ElMessage.success('寝室顺序已保存')
  } catch (error: any) { ElMessage.error(error.message) }
  finally { dormBusy.value = false }
}

// ── Student actions ──
async function loadStudents() {
  studentBusy.value = true
  try {
    const res = await api<{ students: { id: string; stu_name: string }[] }>('/api/students', { cache: 'no-store' })
    students.value = res.students
  } catch (error: any) { ElMessage.error(error.message) }
  finally { studentBusy.value = false }
}
function startEditStudent(s: { id: string; stu_name: string }) {
  editingStudent.value = s
  studentEditForm.stu_name = s.stu_name
  studentEditForm.id = s.id
}
function cancelEditStudent() {
  editingStudent.value = null
  studentEditForm.stu_name = ''
  studentEditForm.id = ''
}
async function submitEditStudent() {
  if (!editingStudent.value) return
  studentBusy.value = true
  try {
    await api(`/api/students/${editingStudent.value.id}`, { method: 'PUT', body: JSON.stringify(studentEditForm) })
    ElMessage.success('学生信息已更新')
    cancelEditStudent()
    await loadStudents()
  } catch (error: any) { ElMessage.error(error.message) }
  finally { studentBusy.value = false }
}
async function deleteStudent(s: { id: number; name: string }) {
  try {
    await ElMessageBox.confirm(`确定要删除学生 "${s.stu_name}" 吗？`, '确认删除', { type: 'warning', confirmButtonText: '删除', cancelButtonText: '取消' })
  } catch { return }
  studentBusy.value = true
  try {
    await api(`/api/students/${s.id}`, { method: 'DELETE' })
    ElMessage.success('学生已删除')
    await loadStudents()
  } catch (error: any) { ElMessage.error(error.message) }
  finally { studentBusy.value = false }
}
async function importStudents(file: File) {
  studentBusy.value = true
  importResult.value = null
  try {
    const form = new FormData()
    form.append('file', file)
    const res = await api<{ imported: number; errors?: string[] }>('/api/students/import', { method: 'POST', body: form })
    importResult.value = res
    if (res.imported > 0) ElMessage.success(`成功导入 ${res.imported} 名学生`)
    if (res.errors && res.errors.length > 0) ElMessage.warning(`${res.errors.length} 条数据导入失败`)
    await loadStudents()
  } catch (error: any) { ElMessage.error(error.message) }
  finally { studentBusy.value = false }
}
async function downloadTemplate() {
  try {
    const resp = await fetch('/api/students/template', { credentials: 'same-origin' })
    if (!resp.ok) throw new Error('下载失败')
    const blob = await resp.blob()
    const url = URL.createObjectURL(blob)
    const a = document.createElement('a')
    a.href = url
    a.download = '学生信息导入-模板.xlsx'
    document.body.appendChild(a)
    a.click()
    document.body.removeChild(a)
    URL.revokeObjectURL(url)
  } catch(e: any) { ElMessage.error(e.message) }
}

// ── Deduction actions ──
async function loadDeductions() {
  deductionBusy.value = true
  try {
    const res = await api<{ records: Deduction[] }>('/api/deductions')
    deductions.value = res.records
  } catch (error: any) { ElMessage.error(error.message) }
  finally { deductionBusy.value = false }
}
async function loadWorkspaceStats() {
  workspaceBusy.value = true
  try {
    const [stats, semesterList] = await Promise.all([
      api<{ stats: WorkspaceStats }>('/api/workspace/stats', { cache: 'no-store' }),
      api<{ semesters: Semester[] }>('/api/semesters', { cache: 'no-store' })
    ])
    workspaceStats.value = stats.stats
    semesters.value = semesterList.semesters
  }
  catch (error: any) { ElMessage.error(error.message) }
  finally { workspaceBusy.value = false }
}
async function openWorkspaceRecords(kind: 'out-of-semester' | 'unassigned' | 'multi-without-subrecords') {
  workspaceBusy.value = true
  try {
    const response = await api<{ records: WorkspaceRecords }>(`/api/workspace/${kind}-records`, { cache: 'no-store' })
    workspaceRecords.value = response.records
    workspaceRecordsTitle.value = kind === 'out-of-semester'
      ? '未在管理范围内的扣分记录'
      : kind === 'multi-without-subrecords'
        ? '无子项寝室整体差'
        : '未指定项目'
    workspaceRecordsVisible.value = true
  } catch (error: any) { ElMessage.error(error.message) }
  finally { workspaceBusy.value = false }
}
async function openWorkspaceRecognition(record: Deduction) {
  workspaceRecordsVisible.value = false
  await startEditRecognition(record)
}
async function openWorkspaceSubrecords(record: MultiDeduction) {
  workspaceRecordsVisible.value = false
  await openSubrecords(record)
}
const activeSemesterNow = computed(() => {
  const now = currentTime.value.getTime()
  return semesters.value.find((semester) => {
    const start = new Date(`${semester.start_time}T00:00:00`).getTime()
    const end = new Date(`${semester.end_time}T23:59:59.999`).getTime()
    return !Number.isNaN(start) && !Number.isNaN(end) && now >= start && now <= end
  }) || null
})
function openStudentBatchDelete() { studentBatchDeleteSelection.value = []; studentBatchDeleteVisible.value = true }
function addStudentBatchDeleteField() { studentBatchDeleteFilters.fields.push({ field: 'id', value: '' }) }
function removeStudentBatchDeleteField(index: number) { if (studentBatchDeleteFilters.fields.length > 1) studentBatchDeleteFilters.fields.splice(index, 1) }
function toggleStudentBatchDeleteSelection(checked: boolean) { studentBatchDeleteSelection.value = checked ? studentBatchDeleteCandidates.value.map((student) => student.id) : [] }
async function submitStudentBatchDelete() {
  if (!studentBatchDeleteSelection.value.length) return ElMessage.warning('请选择要删除的学生')
  try { await ElMessageBox.confirm(`确定删除选中的 ${studentBatchDeleteSelection.value.length} 名学生吗？此操作不可撤销。`, '确认批量删除', { type: 'warning', confirmButtonText: '删除', cancelButtonText: '取消' }) } catch { return }
  studentBusy.value = true
  try {
    const result = await api<{ deleted: number }>('/api/students/batch-delete', { method: 'POST', body: JSON.stringify({ ids: studentBatchDeleteSelection.value }) })
    ElMessage.success(`已删除 ${result.deleted} 名学生`)
    studentBatchDeleteVisible.value = false
    studentBatchDeleteSelection.value = []
    await loadStudents()
  } catch (error: any) { ElMessage.error(error.message) }
  finally { studentBusy.value = false }
}
async function startEditRecognition(r: Deduction) {
  if (students.value.length === 0) await loadStudents()
  editingRecognition.value = r
  recognizedStudentIDs.value = [...(r.recognized_student_ids || [])]
}
function cancelEditRecognition() {
  editingRecognition.value = null
  recognizedStudentIDs.value = []
}
async function submitEditRecognition() {
  if (!editingRecognition.value) return
  deductionBusy.value = true
  try {
    await api(`/api/deductions/${editingRecognition.value.id}/recognition`, { method: 'PUT', body: JSON.stringify({ student_ids: recognizedStudentIDs.value }) })
    ElMessage.success('认定已更新')
    cancelEditRecognition()
    await loadDeductions()
  } catch (error: any) { ElMessage.error(error.message) }
  finally { deductionBusy.value = false }
}
function startEditDeduction(r: { id: string; submit_date: string; student_name: string; content: string; score: string }) {
  editingDeduction.value = r
  deductionEditForm.id = r.id
  deductionEditForm.submit_date = r.submit_date
  deductionEditForm.student_name = r.student_name
  deductionEditForm.content = r.content
  deductionEditForm.score = r.score
}
function cancelEditDeduction() {
  editingDeduction.value = null
  deductionEditForm.id = ''
  deductionEditForm.submit_date = ''
  deductionEditForm.student_name = ''
  deductionEditForm.content = ''
  deductionEditForm.score = ''
}
async function submitEditDeduction() {
  if (!editingDeduction.value) return
  deductionBusy.value = true
  try {
    await api(`/api/deductions/${editingDeduction.value.id}`, { method: 'PUT', body: JSON.stringify(deductionEditForm) })
    ElMessage.success('记录已更新')
    cancelEditDeduction()
    await loadDeductions()
  } catch (error: any) { ElMessage.error(error.message) }
  finally { deductionBusy.value = false }
}
async function deleteDeduction(r: { id: number; record_id: string }) {
  try {
    await ElMessageBox.confirm(`确定要删除记录 "${r.id}" 吗？`, '确认删除', { type: 'warning', confirmButtonText: '删除', cancelButtonText: '取消' })
  } catch { return }
  deductionBusy.value = true
  try {
    await api(`/api/deductions/${r.id}`, { method: 'DELETE' })
    ElMessage.success('记录已删除')
    await loadDeductions()
  } catch (error: any) { ElMessage.error(error.message) }
  finally { deductionBusy.value = false }
}
function openBatchDelete() {
  batchDeleteSelection.value = []
  batchDeleteVisible.value = true
}
function addBatchDeleteField() {
  batchDeleteFilters.fields.push({ field: 'id', value: '' })
}
function removeBatchDeleteField(index: number) {
  if (batchDeleteFilters.fields.length > 1) batchDeleteFilters.fields.splice(index, 1)
}
function toggleBatchDeleteSelection(checked: boolean) {
  batchDeleteSelection.value = checked ? batchDeleteCandidates.value.map((record) => record.id) : []
}
async function submitBatchDelete() {
  if (batchDeleteSelection.value.length === 0) return ElMessage.warning('请选择要删除的记录')
  try {
    await ElMessageBox.confirm(`确定删除选中的 ${batchDeleteSelection.value.length} 条扣分记录吗？此操作不可撤销。`, '确认批量删除', { type: 'warning', confirmButtonText: '删除', cancelButtonText: '取消' })
  } catch { return }
  deductionBusy.value = true
  try {
    const result = await api<{ deleted: number }>('/api/deductions/batch-delete', { method: 'POST', body: JSON.stringify({ ids: batchDeleteSelection.value }) })
    ElMessage.success(`已删除 ${result.deleted} 条记录`)
    batchDeleteVisible.value = false
    batchDeleteSelection.value = []
    await loadDeductions()
  } catch (error: any) { ElMessage.error(error.message) }
  finally { deductionBusy.value = false }
}
async function importDeductions(file: File) {
  deductionBusy.value = true
  deductionImportResult.value = null
  try {
    const form = new FormData()
    form.append('file', file)
    const res = await api<{ imported: number; errors?: string[] }>('/api/deductions/import', { method: 'POST', body: form })
    deductionImportResult.value = res
    if (res.imported > 0) ElMessage.success(`成功导入 ${res.imported} 条记录`)
    if (res.errors && res.errors.length > 0) ElMessage.warning(`${res.errors.length} 条数据导入失败`)
    await loadDeductions()
  } catch (error: any) { ElMessage.error(error.message) }
  finally { deductionBusy.value = false }
}
async function downloadDeductionTemplate() {
  try {
    const resp = await fetch('/api/deductions/template', { credentials: 'same-origin' })
    if (!resp.ok) throw new Error('下载失败')
    const blob = await resp.blob()
    const url = URL.createObjectURL(blob)
    const a = document.createElement('a')
    a.href = url
    a.download = '警务化单项扣分导入-模板.xlsx'
    document.body.appendChild(a)
    a.click()
    document.body.removeChild(a)
    URL.revokeObjectURL(url)
  } catch(e: any) { ElMessage.error(e.message) }
}

async function loadMultiDeductions() {
  multiDeductionBusy.value = true
  try { multiDeductions.value = (await api<{ records: MultiDeduction[] }>('/api/multi-deductions')).records }
  catch (error: any) { ElMessage.error(error.message) }
  finally { multiDeductionBusy.value = false }
}
function startEditMultiDeduction(record: MultiDeduction) { editingMultiDeduction.value = record; multiDeductionForm.submit_date = record.submit_date; multiDeductionForm.dorm_name = record.dorm_name; multiDeductionForm.content = record.content; multiDeductionForm.score = record.score }
function cancelEditMultiDeduction() { editingMultiDeduction.value = null }
async function submitEditMultiDeduction() { if (!editingMultiDeduction.value) return; multiDeductionBusy.value = true; try { await api(`/api/multi-deductions/${editingMultiDeduction.value.id}`, { method: 'PUT', body: JSON.stringify(multiDeductionForm) }); cancelEditMultiDeduction(); await loadMultiDeductions(); ElMessage.success('记录已更新') } catch (error: any) { ElMessage.error(error.message) } finally { multiDeductionBusy.value = false } }
function onMultiDeductionSelectionChange(records: MultiDeduction[]) { selectedMultiDeductionIDs.value = records.map((record) => record.id) }
function openMultiBatchDelete() { multiBatchDeleteSelection.value = []; multiBatchDeleteVisible.value = true }
function addMultiBatchField() { multiBatchDeleteFilters.fields.push({ field: 'id', value: '' }) }
function removeMultiBatchField(index: number) { if (multiBatchDeleteFilters.fields.length > 1) multiBatchDeleteFilters.fields.splice(index, 1) }
function toggleMultiBatchSelection(checked: boolean) { multiBatchDeleteSelection.value = checked ? multiBatchDeleteCandidates.value.map((record) => record.id) : [] }
async function submitMultiBatchDelete() { if (!multiBatchDeleteSelection.value.length) return ElMessage.warning('请选择要删除的记录'); selectedMultiDeductionIDs.value = [...multiBatchDeleteSelection.value]; await deleteSelectedMultiDeductions(); if (!selectedMultiDeductionIDs.value.length) multiBatchDeleteVisible.value = false }
async function deleteSelectedMultiDeductions() { if (!selectedMultiDeductionIDs.value.length) return ElMessage.warning('请选择要删除的记录'); try { await ElMessageBox.confirm(`确定删除选中的 ${selectedMultiDeductionIDs.value.length} 条寝室整体差记录吗？子项也会一并删除。`, '确认批量删除', { type: 'warning', confirmButtonText: '删除', cancelButtonText: '取消' }) } catch { return }; multiDeductionBusy.value = true; try { const result = await api<{ deleted: number }>('/api/multi-deductions/batch-delete', { method: 'POST', body: JSON.stringify({ ids: selectedMultiDeductionIDs.value }) }); selectedMultiDeductionIDs.value = []; await loadMultiDeductions(); ElMessage.success(`已删除 ${result.deleted} 条记录`) } catch (error: any) { ElMessage.error(error.message) } finally { multiDeductionBusy.value = false } }
async function deleteMultiDeduction(record: MultiDeduction) {
  try { await ElMessageBox.confirm(`确定删除寝室 "${record.dorm_name}" 的这条记录吗？子项也会一并删除。`, '确认删除', { type: 'warning', confirmButtonText: '删除', cancelButtonText: '取消' }) } catch { return }
  multiDeductionBusy.value = true
  try {
    const result = await api<{ deleted: number }>('/api/multi-deductions/batch-delete', { method: 'POST', body: JSON.stringify({ ids: [record.id] }) })
    await loadMultiDeductions()
    ElMessage.success(`已删除 ${result.deleted} 条记录`)
  } catch (error: any) { ElMessage.error(error.message) }
  finally { multiDeductionBusy.value = false }
}
async function importMultiDeductions(file: File) {
  multiDeductionBusy.value = true
  try {
    const form = new FormData(); form.append('file', file)
    const result = await api<{ imported: number; errors?: string[] }>('/api/multi-deductions/import', { method: 'POST', body: form })
    ElMessage.success(`成功导入 ${result.imported} 条寝室整体差记录`)
    if (result.errors?.length) ElMessage.warning(`${result.errors.length} 条数据导入失败`)
    await loadMultiDeductions()
  } catch (error: any) { ElMessage.error(error.message) }
  finally { multiDeductionBusy.value = false }
}
async function downloadMultiDeductionTemplate() {
  try {
    const response = await fetch('/api/multi-deductions/template', { credentials: 'same-origin' })
    if (!response.ok) throw new Error('下载失败')
    const url = URL.createObjectURL(await response.blob()); const link = document.createElement('a')
    link.href = url; link.download = '警务化多项扣分导入-模板.xlsx'; link.click(); URL.revokeObjectURL(url)
  } catch (error: any) { ElMessage.error(error.message) }
}
async function openSubrecords(record: MultiDeduction) {
  if (!students.value.length) await loadStudents()
  managingSubrecords.value = record
  await loadMultiSubrecords(record.id)
  resetMultiSubrecordForm()
}
async function loadMultiSubrecords(recordID: string) {
  multiSubrecords.value = (await api<{ subrecords: MultiSubrecord[] }>(`/api/multi-deductions/${recordID}/subrecords`)).subrecords
}
function resetMultiSubrecordForm() { multiSubrecordForm.id = ''; multiSubrecordForm.content = ''; multiSubrecordForm.student_ids = [] }
function editMultiSubrecord(subrecord: MultiSubrecord) { multiSubrecordForm.id = subrecord.id; multiSubrecordForm.content = subrecord.content; multiSubrecordForm.student_ids = [...subrecord.student_ids] }
async function saveMultiSubrecord() {
  if (!managingSubrecords.value) return
  multiDeductionBusy.value = true
  try {
    await api(`/api/multi-deductions/${managingSubrecords.value.id}/subrecords`, { method: 'PUT', body: JSON.stringify(multiSubrecordForm) })
    await loadMultiSubrecords(managingSubrecords.value.id); resetMultiSubrecordForm(); ElMessage.success('子项已保存')
  } catch (error: any) { ElMessage.error(error.message) }
  finally { multiDeductionBusy.value = false }
}
async function deleteMultiSubrecord(subrecord: MultiSubrecord) {
  if (!managingSubrecords.value) return
  try { await ElMessageBox.confirm('确定删除该子项吗？', '确认删除', { type: 'warning', confirmButtonText: '删除', cancelButtonText: '取消' }) } catch { return }
  try { await api(`/api/multi-deductions/${managingSubrecords.value.id}/subrecords/${subrecord.id}`, { method: 'DELETE' }); await loadMultiSubrecords(managingSubrecords.value.id); resetMultiSubrecordForm() }
  catch (error: any) { ElMessage.error(error.message) }
}
</script>

<template>
  <div v-if="page === 'login'" class="auth-shell">
    <section class="auth-brand">
      <img src="/icon.png" alt="" style="width:100px;height:100px;object-fit:contain" />
      <div><h1>纪检工作台</h1><p>Police Style Workspace</p></div>
    </section>
    <main class="auth-right-wrap">
      <ElForm class="form art-card" label-position="top" @submit.prevent="submitLogin">
        <h2 class="title">欢迎登录</h2><p class="sub-title">请输入管理员账号和密码</p>
        <ElFormItem label="用户名"><ElInput v-model.trim="login.username" autocomplete="username" /></ElFormItem>
        <ElFormItem label="密码"><ElInput v-model="login.password" type="password" show-password autocomplete="current-password" @keyup.enter="submitLogin" /></ElFormItem>
        <ElButton class="custom-height" type="primary" :loading="busy" native-type="submit">登录</ElButton>
      </ElForm>
    </main>
    <SiteFooter />
  </div>

  <div v-else-if="authReady" class="layout-shell">
    <aside class="layout-sidebar">
      <div class="sidebar-brand"><img src="/icon.png" alt="" style="width:50px;height:50px;object-fit:contain" /><strong>纪检工作台</strong></div>
      <nav><a :class="{ active: page === 'workspace' }" href="/workspace">工作台</a><a :class="{ active: page === 'daily-management' }" href="/daily-management">日常综合管理</a><a :class="{ active: page === 'dorms' }" href="/dorms">寝室管理</a><a :class="{ active: page === 'semester' }" href="/semester">学期管理</a><a :class="{ active: page === 'students' }" href="/students">学生管理</a><a :class="{ active: page === 'deductions' }" href="/deductions">常规扣分记录管理</a><a :class="{ active: page === 'multi-deductions' }" href="/multi-deductions">寝室整体差扣分记录管理</a><a :class="{ active: page === 'daily-report' }" href="/daily-report">警务化管理每日播报</a><a :class="{ active: page === 'password' }" href="/change-password">修改密码</a></nav>
      <div class="sidebar-spacer"></div>
      <ElButton class="sidebar-logout" text @click="logout">退出登录</ElButton>
    </aside>
    <div class="layout-main">
      <header class="top-bar"><strong>{{ page === 'daily-report' ? '警务化管理每日播报' : page === 'password' ? '修改密码' : page === 'daily-management' ? '日常综合管理' : page === 'dorms' ? '寝室管理' : page === 'students' ? '学生管理' : page === 'deductions' ? '常规扣分记录管理' : page === 'multi-deductions' ? '寝室整体差扣分记录管理' : page === 'semester' ? '学期管理' : '工作台' }}</strong><time class="top-bar-clock">{{ currentTimeText }}</time></header>
      <main class="page-area">
        <!-- 申诉模板导出弹窗：所有页面共享（常规扣分/整体差/日常管理等操作列均可打开） -->
        <ElDialog v-model="appealDialogVisible" :title="appealIsSchool ? '校督申诉模板导出' : '大队督察申诉模板导出'" width="620px" @close="appealRecord = null">
          <div v-if="appealRecord" v-loading="appealSaving">
            <ElForm label-position="top" label-width="auto">
              <ElFormItem label="日期"><ElInput :model-value="appealRecord.date.slice(5).replace('-', '.')" disabled /></ElFormItem>
              <ElFormItem label="项目名称"><ElInput :model-value="appealRecord.content" disabled /></ElFormItem>
              <ElFormItem label="姓名"><ElInput :model-value="appealRecord.student_names" disabled /></ElFormItem>
              <ElFormItem label="大队"><ElInput v-model="appealGrade" placeholder="大队" /></ElFormItem>
              <ElFormItem label="区队"><ElInput v-model="appealClass" placeholder="区队" /></ElFormItem>
              <ElFormItem label="学生复议情况说明"><ElInput v-model="appealText" type="textarea" :rows="3" placeholder="请填写复议情况说明" /></ElFormItem>
              <template v-if="!appealIsSchool">
                <ElFormItem label="大督扣分照片">
                  <div style="display:flex;flex-wrap:wrap;gap:6px;margin-bottom:8px"><div v-for="(p,i) in appealDdPhotos" :key="i" style="display:flex;align-items:center;gap:4px;background:#f5f7fa;padding:4px 8px;border-radius:4px"><span style="font-size:12px;max-width:140px;overflow:hidden;text-overflow:ellipsis;white-space:nowrap">{{ p }}</span><ElButton text type="danger" size="small" @click="deleteAppealPhoto(p, 'dd')">×</ElButton></div></div>
                  <ElButton size="small" @click="uploadAppealPhoto('dd')">上传照片</ElButton>
                </ElFormItem>
                <ElFormItem label="申诉照片">
                  <div style="display:flex;flex-wrap:wrap;gap:6px;margin-bottom:8px"><div v-for="(p,i) in appealAppealPhotos" :key="i" style="display:flex;align-items:center;gap:4px;background:#f5f7fa;padding:4px 8px;border-radius:4px"><span style="font-size:12px;max-width:140px;overflow:hidden;text-overflow:ellipsis;white-space:nowrap">{{ p }}</span><ElButton text type="danger" size="small" @click="deleteAppealPhoto(p, 'appeal')">×</ElButton></div></div>
                  <ElButton size="small" @click="uploadAppealPhoto('appeal')">上传照片</ElButton>
                </ElFormItem>
              </template>
              <template v-else>
                <ElFormItem label="申诉照片">
                  <div style="display:flex;flex-wrap:wrap;gap:6px;margin-bottom:8px"><div v-for="(p,i) in appealAppealPhotos" :key="i" style="display:flex;align-items:center;gap:4px;background:#f5f7fa;padding:4px 8px;border-radius:4px"><span style="font-size:12px;max-width:140px;overflow:hidden;text-overflow:ellipsis;white-space:nowrap">{{ p }}</span><ElButton text type="danger" size="small" @click="deleteAppealPhoto(p, 'appeal')">×</ElButton></div></div>
                  <ElButton size="small" @click="uploadAppealPhoto('appeal')">上传照片</ElButton>
                </ElFormItem>
              </template>
            </ElForm>
            <div style="display:flex;gap:10px;justify-content:flex-end;margin-top:16px">
              <ElButton @click="appealDialogVisible = false">取消</ElButton>
              <ElButton type="primary" :loading="appealSaving" @click="saveAppealConfig()">保存</ElButton>
              <ElButton type="success" :loading="appealExporting" :disabled="appealExporting" @click="exportAppealZip()">{{ appealExporting ? '请稍候...' : '导出' }}</ElButton>
            </div>
          </div>
        </ElDialog>
        <section v-if="page === 'workspace'" class="page-content workspace-page" v-loading="workspaceBusy">
          <div class="workspace-heading"><div><h1>纪检工作台</h1><p>扣分记录与认定情况概览</p></div></div>
          <div class="workspace-stat-grid">
            <article class="workspace-stat-card stat-blue"><span class="workspace-stat-label">常规扣分数目</span><strong>{{ workspaceStats.single_deduction_count }}</strong><small>条常规扣分记录</small></article>
            <article class="workspace-stat-card stat-green"><span class="workspace-stat-label">寝室整体差扣分数目</span><strong>{{ workspaceStats.multi_deduction_count }}</strong><small>条寝室整体差记录</small></article>
            <button class="workspace-stat-card stat-orange workspace-card-button" @click="openWorkspaceRecords('multi-without-subrecords')"><span class="workspace-stat-label">无子项寝室整体差</span><strong>{{ workspaceStats.multi_without_subrecords_count }}</strong><small>点击查看具体条目</small></button>
            <article class="workspace-stat-card stat-orange"><span class="workspace-stat-label">总扣分</span><strong>{{ workspaceStats.total_score }}</strong><small>两类记录分数合计</small></article>
            <button class="workspace-stat-card stat-red workspace-card-button" @click="openWorkspaceRecords('unassigned')"><span class="workspace-stat-label">未指定项目数量</span><strong>{{ workspaceStats.unassigned_count }}</strong><small>未认定记录与未指定子项</small></button>
            <button class="workspace-stat-card workspace-card-button stat-slate" @click="openWorkspaceRecords('out-of-semester')"><span class="workspace-stat-label">未在管理范围内扣分记录数</span><strong>{{ workspaceStats.out_of_semester_count }}</strong><small>点击查看两类扣分记录</small></button>
            <article v-if="activeSemesterNow" class="workspace-stat-card current-semester-card"><span class="workspace-stat-label">当前学期</span><strong>{{ activeSemesterNow.semester_name }}</strong><small>{{ activeSemesterNow.start_time }} 至 {{ activeSemesterNow.end_time }}</small></article>
            <article v-if="workspaceStats.duty_dorm_name" class="workspace-stat-card duty-dorm-card"><span class="workspace-stat-label">本周包干区负责寝室</span><strong>{{ workspaceStats.duty_dorm_name }}</strong><small>按当前学期轮值顺序安排</small></article>
          </div>
          <ElDialog v-model="workspaceRecordsVisible" :title="workspaceRecordsTitle" width="860px">
            <div class="workspace-record-section"><h3>常规扣分记录（{{ workspaceRecords.single.length }}）</h3><ElTable :data="workspaceRecords.single" border stripe max-height="220"><ElTableColumn prop="submit_date" label="日期" width="165" /><ElTableColumn prop="student_name" label="姓名" width="130" /><ElTableColumn prop="content" label="扣分内容" min-width="220" /><ElTableColumn prop="score" label="分数" width="80" /></ElTable></div>
            <div class="workspace-record-section"><h3>寝室整体差扣分记录（{{ workspaceRecords.multi.length }}）</h3><ElTable :data="workspaceRecords.multi" border stripe max-height="220"><ElTableColumn prop="submit_date" label="日期" width="165" /><ElTableColumn prop="dorm_name" label="寝室名称" width="130" /><ElTableColumn prop="content" label="扣分项目" min-width="220" /><ElTableColumn prop="score" label="分数" width="80" /></ElTable></div>
          </ElDialog>
        </section>

        <section v-else-if="page === 'daily-report'" class="page-content art-card">
          <div class="semester-toolbar"><h2 class="semester-title">警务化管理每日播报</h2><div style="display:flex;gap:10px"><span style="font-size:0.75em">全局{{ reportConfig.set_status ? '启用' : '禁用' }}</span><ElButton :disabled="dailyReportBusy" @click="reportDateVisible=true">选择日期播报</ElButton><ElButton :loading="dailyReportBusy" :disabled="dailyReportBusy" @click="runDailyReport">{{ dailyReportBusy ? '请稍候' : '立即播报' }}</ElButton><ElButton @click="reportConfigVisible=true">播报配置</ElButton><ElButton @click="robotVisible=true">钉钉机器人管理</ElButton></div></div>
          <div class="daily-semester-grid"><article v-for="robot in reportRobots" :key="robot.robot_name" class="daily-semester-card"><span>钉钉机器人</span><strong>{{ robot.robot_name }}</strong><small>{{ robot.set_status ? '已启用' : '已禁用' }}</small></article></div>
          <section class="workspace-record-section" style="margin-top:24px"><h3>播报日志</h3><ElTable :data="reportLogs" border stripe max-height="360"><ElTableColumn prop="op_time" label="操作时间" width="190" /><ElTableColumn prop="robot_name" label="机器人名称" width="160" /><ElTableColumn prop="op_status" label="播报状态" width="140" /><ElTableColumn prop="fetch_content" label="播报内容" min-width="360" show-overflow-tooltip /><ElTableColumn label="操作" width="170" fixed="right"><template #default="{ row }"><ElButton type="primary" text @click="exportReportLog(row)">导出记录</ElButton><ElButton type="danger" text @click="deleteReportLog(row)">删除</ElButton></template></ElTableColumn></ElTable></section>
          <ElDialog v-model="reportDateVisible" title="选择日期播报" width="420px"><ElForm label-position="top"><ElFormItem label="播报日期"><ElDatePicker v-model="selectedReportDate" type="date" value-format="YYYY-MM-DD" placeholder="请选择日期" style="width:100%" /></ElFormItem></ElForm><template #footer><ElButton @click="reportDateVisible=false">取消</ElButton><ElButton type="primary" :loading="dailyReportBusy" @click="runDailyReportForDate">确认播报</ElButton></template></ElDialog>
          <ElDialog v-model="reportConfigVisible" title="播报配置" width="600px"><ElForm label-position="top"><ElFormItem label="全局状态"><ElSwitch v-model="reportConfig.set_status" :active-value="1" :inactive-value="0" active-text="全局启用" inactive-text="全局禁用" /></ElFormItem><ElFormItem label="VPN 登录地址"><ElInput v-model="reportConfig.vpn_login_url" /></ElFormItem><ElFormItem label="VPN 用户名"><ElInput v-model="reportConfig.username_vpn" /></ElFormItem><ElFormItem label="VPN 密码"><ElInput v-model="reportConfig.password_vpn" show-password /></ElFormItem><ElFormItem label="内网警务化管理服务器地址"><ElInput v-model="reportConfig.vpn_police_style_server_url" /></ElFormItem><ElFormItem label="服务器用户名"><ElInput v-model="reportConfig.username_police_style_server" /></ElFormItem><ElFormItem label="服务器密码"><ElInput v-model="reportConfig.password_police_style_server" show-password /></ElFormItem><ElFormItem label="每日播报时间"><ElTimePicker v-model="reportConfig.fetch_time_everyday" format="HH:mm" value-format="HH:mm" /></ElFormItem></ElForm><template #footer><ElButton @click="reportConfigVisible=false">取消</ElButton><ElButton type="primary" @click="saveDailyReport();reportConfigVisible=false">保存</ElButton></template></ElDialog>
          <ElDialog v-model="robotVisible" title="钉钉机器人管理" width="980px" @close="editingRobot=null"><ElForm inline><ElFormItem label="机器人名称"><ElInput v-model="robotForm.robot_name" /></ElFormItem><ElFormItem label="机器人地址"><ElInput v-model="robotForm.dingtalk_webbook_url" /></ElFormItem><ElFormItem label="加签密钥"><ElInput v-model="robotForm.dingtalk_webbook_password" show-password /></ElFormItem><ElFormItem><ElButton type="primary" @click="saveRobot">新增机器人</ElButton></ElFormItem></ElForm><ElTable :data="reportRobots" border stripe><ElTableColumn prop="robot_name" label="机器人名称" width="160" /><ElTableColumn label="机器人地址" min-width="300"><template #default="{ row }"><ElInput v-if="editingRobot && editingRobot.robot_name===row.robot_name" v-model="editingRobot.dingtalk_webbook_url" /><span v-else>{{ maskRobotValue(row.dingtalk_webbook_url) }}</span></template></ElTableColumn><ElTableColumn label="加签密钥" min-width="180"><template #default="{ row }"><ElInput v-if="editingRobot && editingRobot.robot_name===row.robot_name" v-model="editingRobot.dingtalk_webbook_password" type="password" /><span v-else>{{ maskRobotValue(row.dingtalk_webbook_password, true) }}</span></template></ElTableColumn><ElTableColumn label="启用" width="90"><template #default="{ row }"><ElSwitch v-model="row.set_status" :active-value="1" :inactive-value="0" @change="toggleRobot(row)" /></template></ElTableColumn><ElTableColumn label="操作" width="140"><template #default="{ row }"><ElButton type="primary" text @click.stop="editingRobot && editingRobot.robot_name===row.robot_name ? saveEditingRobot() : editRobot(row)">{{ editingRobot && editingRobot.robot_name===row.robot_name ? '保存' : '编辑' }}</ElButton><ElButton type="danger" text @click.stop="deleteRobot(row)">删除</ElButton></template></ElTableColumn></ElTable></ElDialog>
        </section>
        <section v-else-if="page === 'daily-management'" class="page-content daily-management-page">
          <template v-if="!dailyManagementSemester">
            <div class="workspace-heading"><div><h1>日常综合管理</h1><p>选择学期进入对应的日常管理内容</p></div></div>
            <div v-if="semesters.length" class="daily-semester-grid"><button v-for="semester in semesters" :key="semester.semester_name" class="daily-semester-card" @click="openDailyManagementSemester(semester)"><span>学期</span><strong>{{ semester.semester_name }}</strong><small>{{ semester.start_time }} 至 {{ semester.end_time }}</small></button></div>
            <div v-else class="daily-management-empty">暂无学期</div>
          </template>
          <template v-else>
            <div class="semester-toolbar"><div><h2 class="semester-title">{{ dailyManagementSemester.semester_name }}</h2><p class="daily-detail-date">{{ dailyManagementSemester.start_time }} 至 {{ dailyManagementSemester.end_time }}</p></div><div style="display:flex;gap:10px"><ElButton v-if="showWeekRecords" type="success" :loading="weekBatchExportBusy" @click="batchExportWeekAppeals">批量导出</ElButton><ElButton @click="toggleWeekRecords">{{ showWeekRecords ? '返回周详情' : '本周扣分条目汇总' }}</ElButton><ElButton @click="dailySummaryVisible ? closeDailySummary() : openDailySummary()">{{ dailySummaryVisible ? '返回周详情' : '学期汇总' }}</ElButton><ElButton @click="dailySummaryVisible ? exportDailySummary() : openDailyWeekExport()">导出 XLSX</ElButton><ElButton @click="closeDailyManagementSemester">返回学期列表</ElButton></div></div>
            <div class="daily-management-detail" v-loading="dailyManagementBusy">
              <aside class="daily-week-list"><button v-for="week in dailyWeeks" :key="week.index" :class="{ active: selectedDailyWeek === week.index }" @click="selectDailyWeek(week.index)">第 {{ week.index + 1 }} 周<small>{{ week.start }} 至 {{ week.end }}</small></button></aside>
              <div v-if="showWeekRecords" class="daily-score-table-wrap" v-loading="weekRecordsBusy">
  <h3 style="margin-top:0">常规扣分项目</h3>
  <ElTable :data="weekRecords.single" border stripe style="width:100%;margin-bottom:18px">
    <ElTableColumn width="52" align="center"><template #header><ElCheckbox :model-value="weekRecords.single.length > 0 && weekBatchSingleIDs.length === weekRecords.single.length" :indeterminate="weekBatchSingleIDs.length > 0 && weekBatchSingleIDs.length < weekRecords.single.length" @change="value => toggleAllWeekSingle(Boolean(value))" /></template><template #default="{ row }"><ElCheckbox :model-value="weekBatchSingleIDs.includes(row.id)" @change="value => toggleWeekSingle(row.id, Boolean(value))" /></template></ElTableColumn>
    <ElTableColumn prop="id" label="记录ID" width="140" />
    <ElTableColumn prop="date" label="日期" width="110" />
    <ElTableColumn prop="content" label="扣分项目" min-width="200" />
    <ElTableColumn prop="score" label="分数" width="80" />
    <ElTableColumn prop="student_names" label="认定学生" min-width="160" />
    <ElTableColumn label="操作" width="120"><template #default="{ row }"><ElButton type="primary" size="small" @click="openAppeal(row)">导出申诉模板</ElButton></template></ElTableColumn>
      </ElTable>
  <h3>寝室整体差扣分项目</h3>
  <ElTable :data="weekRecords.multi" border stripe style="width:100%">
    <ElTableColumn width="52" align="center"><template #header><ElCheckbox :model-value="weekRecords.multi.length > 0 && weekBatchMultiIDs.length === weekRecords.multi.length" :indeterminate="weekBatchMultiIDs.length > 0 && weekBatchMultiIDs.length < weekRecords.multi.length" @change="value => toggleAllWeekMulti(Boolean(value))" /></template><template #default="{ row }"><ElCheckbox :model-value="weekBatchMultiIDs.includes(row.id)" @change="value => toggleWeekMulti(row.id, Boolean(value))" /></template></ElTableColumn>
    <ElTableColumn type="expand">
      <template #default="{ row }">
        <ElTable :data="row.subs" border size="small" style="width:100%">
          <ElTableColumn prop="content" label="子项内容" min-width="200" />
          <ElTableColumn prop="student_names" label="负责学生" min-width="160" />
        </ElTable>
      </template>
    </ElTableColumn>
    <ElTableColumn prop="id" label="记录ID" width="140" />
    <ElTableColumn prop="date" label="日期" width="110" />
    <ElTableColumn prop="content" label="扣分项目" min-width="220" />
    <ElTableColumn prop="score" label="分数" width="80" />
    <ElTableColumn label="操作" width="120"><template #default="{ row }"><ElButton type="primary" size="small" @click="openAppeal(row)">导出申诉模板</ElButton></template></ElTableColumn>
      </ElTable>
</div>
<div v-else class="daily-score-table-wrap"><div v-if="!dailySummaryVisible" class="daily-discipline-list"><strong>第 {{ (selectedDailyWeek ?? 0) + 1 }} 周个人惩戒名单：</strong><strong v-if="!punishmentList.length">无</strong><template v-else><template v-for="(entry, i) in punishmentList" :key="entry.student_id"><ElButton type="primary" link @click="openPunishmentDetail(entry)">{{ entry.student_name }}</ElButton><span v-if="i < punishmentList.length - 1">、</span></template></template></div><ElTable :data="dailySummaryVisible ? dailySummaryRows : dailyWeekData.rows" :row-class-name="dailySummaryVisible ? undefined : dailyRowClassName" border stripe style="width:100%"><ElTableColumn prop="id" label="学号" width="115" fixed="left" /><ElTableColumn prop="name" label="姓名" width="100" fixed="left" /><ElTableColumn v-if="dailySummaryVisible" prop="total" label="总扣分" width="120" fixed="left"><template #default="{ row }">{{ formatSummaryScore(row.total) }}</template></ElTableColumn><template v-if="dailySummaryVisible"><ElTableColumn v-for="week in dailyWeeks" :key="week.index" :label="`第${week.index + 1}周 (${formatDailyDate(week.start)}~${formatDailyDate(week.end)})`" width="180" align="center"><template #default="{ row }">{{ formatSummaryScore(row.scores[`week_${week.index}`] || 0) }}</template></ElTableColumn></template><template v-else><ElTableColumn v-for="date in dailyWeekData.week.dates" :key="date" :label="formatDailyDate(date)" width="96" align="center"><template #default="{ row }">{{ formatDailyCellScore(row.scores[date] || 0) }}</template></ElTableColumn><ElTableColumn label="个人总计" width="120" fixed="right" align="center"><template #default="{ row }">{{ formatSummaryScore(dailyRowTotal(row)) }}</template></ElTableColumn></template></ElTable></div>
            </div>
          </template>
          <ElDialog v-model="punishmentDetailVisible" :title="selectedPunishmentStudent ? '惩戒人：' + selectedPunishmentStudent.student_name : ''" width="760px">
                <div v-if="selectedPunishmentStudent">
                  <p style="margin-bottom:16px;font-size:15px"><strong>计入惩戒分值：</strong>{{ selectedPunishmentStudent.total.toFixed(2) }}</p>
                  <ElTable :data="selectedPunishmentStudent.records" border stripe size="small" style="width:100%">
                    <ElTableColumn prop="record_id" label="ID" width="120" />
                    <ElTableColumn label="日期" width="110"><template #default="{ row }">{{ formatDetailDate(row.date) }}</template></ElTableColumn>
                    <ElTableColumn prop="content" label="扣分内容" min-width="200" />
                    <ElTableColumn label="计入综测分值" width="130" align="center"><template #default="{ row }">{{ row.raw_score.toFixed(3) }}</template></ElTableColumn>
                    <ElTableColumn label="计入惩戒分值" width="130" align="center"><template #default="{ row }">{{ row.logic_score.toFixed(3) }}</template></ElTableColumn>
                  </ElTable>
                </div>
              </ElDialog>
              <ElDialog v-model="dailyExportOrderVisible" title="自定义导出顺序" width="520px">
            <p class="daily-export-order-tip">拖动学生调整导出顺序，未调整的学生将按原顺序追加。</p>
            <div class="dorm-order-list">
              <div v-for="(row, index) in dailyExportOrderRows" :key="row.id" class="dorm-order-item" draggable="true" @dragstart="onDailyExportDragStart(row.id)" @dragover.prevent @drop="onDailyExportDrop(row.id)"><span class="dorm-order-index">{{ index + 1 }}</span><span>{{ row.name }}（{{ row.id }}）</span><span class="dorm-drag-hint">拖动排序</span></div>
            </div>
            <template #footer><ElButton @click="dailyExportOrderVisible = false">取消</ElButton><ElButton type="primary" @click="exportDailyWeek">确认导出</ElButton></template>
          </ElDialog>
        </section>

        <!-- ── Dorm management ── -->
        <section v-else-if="page === 'dorms'" class="page-content art-card students-page-content">
          <div class="semester-toolbar">
            <h2 class="semester-title">寝室管理</h2>
            <div style="display:flex;gap:10px"><ElButton class="custom-height" @click="openDormOrder">调整顺序</ElButton><ElButton type="primary" class="custom-height" @click="openCreateDorm">插入寝室</ElButton></div>
          </div>
          <div class="students-table-wrap">
            <ElTable :data="dorms" v-loading="dormBusy" border stripe style="width:100%">
              <ElTableColumn prop="seq" label="顺序" width="140" />
              <ElTableColumn prop="dorm_name" label="寝室名称" min-width="220" />
              <ElTableColumn label="手机号" min-width="180"><template #default="{ row }">{{ row.phone_number || '未填写' }}</template></ElTableColumn>
              <ElTableColumn label="操作" width="180"><template #default="{ row }"><ElButton type="primary" text size="small" @click="openEditDorm(row)">编辑</ElButton><ElButton type="danger" text size="small" @click="deleteDorm(row)">删除</ElButton></template></ElTableColumn>
            </ElTable>
          </div>
          <ElDialog v-model="dormDialogVisible" :title="editingDorm ? '编辑寝室' : '插入寝室'" width="400px" @closed="editingDorm = null">
            <ElForm label-position="top" @submit.prevent="saveDorm"><ElFormItem label="寝室名称"><ElInput v-model="dormName" /></ElFormItem><ElFormItem label="手机号"><ElInput v-model="dormPhoneNumber" clearable /></ElFormItem><div style="display:flex;justify-content:flex-end;gap:10px"><ElButton @click="dormDialogVisible = false">取消</ElButton><ElButton type="primary" native-type="submit" :loading="dormBusy">保存</ElButton></div></ElForm>
          </ElDialog>
          <ElDialog v-model="dormOrderVisible" title="调整寝室顺序" width="560px" @closed="draggedDormName = ''">
            <div class="dorm-order-list">
              <div v-for="(dorm, index) in sortableDorms" :key="dorm.dorm_name" class="dorm-order-item" draggable="true" @dragstart="onDormDragStart(dorm.dorm_name)" @dragover.prevent @drop="onDormDrop(dorm.dorm_name)"><span class="dorm-order-index">{{ index + 1 }}</span><span>{{ dorm.dorm_name }}<small v-if="dorm.phone_number"> · {{ dorm.phone_number }}</small></span><span class="dorm-drag-hint">拖动排序</span></div>
            </div>
            <template #footer><ElButton @click="dormOrderVisible = false">取消</ElButton><ElButton type="primary" :loading="dormBusy" @click="saveDormOrder">保存顺序</ElButton></template>
          </ElDialog>
        </section>

        <!-- ── Semester management ── -->
        <section v-else-if="page === 'semester'" class="page-content art-card">
          <div class="semester-toolbar">
            <h2 class="semester-title">学期管理</h2>
            <ElButton type="primary" class="custom-height" @click="activeSemester = '__new__'; editing = false">+ 新建学期</ElButton>
          </div>

          <!-- Semester cards -->
          <ElCollapse v-model="activeSemester" accordion>
            <ElCollapseItem
              v-for="s in semesters"
              :key="s.semester_name"
              :name="s.semester_name"
            >
              <template #title>
                <span class="collapse-title">{{ s.semester_name }}</span>
                <span class="collapse-date">{{ s.start_time }} ~ {{ s.end_time }}</span>
              </template>

              <template v-if="editing && activeSemester === s.semester_name">
                <ElForm label-position="top" @submit.prevent="saveSemester">
                  <ElFormItem label="起始日期（周六）">
                    <ElDatePicker
                      :model-value="currentSemester?.start_time"
                      @update:model-value="onEditStartChange"
                      value-format="YYYY-MM-DD"
                      placeholder="选择周六"
                      :disabled-date="(d: Date) => d.getDay() !== 6"
                    />
                  </ElFormItem>
                  <ElFormItem label="结束日期（仅可选周五）">
                    <ElDatePicker
                      :model-value="currentSemester?.end_time"
                      @update:model-value="(val: string) => { if (currentSemester) currentSemester.end_time = val }"
                      value-format="YYYY-MM-DD"
                      placeholder="选择周五"
                      :disabled-date="(d: Date) => d.getDay() !== 5"
                    />
                  </ElFormItem>
                  <div class="semester-actions">
                    <ElButton type="primary" :loading="busy" native-type="submit">保存</ElButton>
                    <ElButton @click="cancelEdit">取消</ElButton>
                  </div>
                </ElForm>
              </template>
              <template v-else>
                <div class="semester-info">
                  <p><span class="label">起始日期（周六）：</span>{{ currentSemester?.start_time }}</p>
                  <p><span class="label">结束日期（周五）：</span>{{ currentSemester?.end_time }}</p>
                </div>
                <div class="semester-actions">
                  <ElButton type="primary" @click="startEdit">编辑学期</ElButton>
                  <ElButton type="danger" @click="deleteSemester">删除学期</ElButton>
                </div>
              </template>
            </ElCollapseItem>
          </ElCollapse>

          <!-- Create new semester -->
          <div v-if="activeSemester === '__new__'" class="semester-detail">
            <ElForm label-position="top" @submit.prevent="createSemester">
              <ElFormItem label="学期名称">
                <ElInput v-model="newSemester.semester_name" placeholder="如：2025秋" />
              </ElFormItem>
              <ElFormItem label="起始日期（仅可选周六）">
                <ElDatePicker
                  v-model="newSemester.start_time"
                  value-format="YYYY-MM-DD"
                  placeholder="选择周六"
                  :disabled-date="(d: Date) => d.getDay() !== 6"
                />
              </ElFormItem>
              <ElFormItem label="结束日期（仅可选周五）">
                <ElDatePicker
                  v-model="newSemester.end_time"
                  value-format="YYYY-MM-DD"
                  placeholder="选择周五"
                  :disabled-date="(d: Date) => d.getDay() !== 5"
                />
              </ElFormItem>
              <ElButton type="primary" :loading="busy" native-type="submit">创建学期</ElButton>
            </ElForm>
          </div>
        </section>

        <!-- ── Student management ── -->
        <section v-else-if="page === 'students'" class="page-content art-card students-page-content">
          <div class="semester-toolbar">
            <h2 class="semester-title">学生管理</h2>
            <div style="display:flex;gap:10px;">
              <ElButton type="primary" class="custom-height" @click="downloadTemplate">模板下载</ElButton>
              <ElUpload :show-file-list="false" :before-upload="(f) => { importStudents(f); return false }" accept=".xlsx">
                <ElButton type="success" class="custom-height" :loading="studentBusy">导入 Excel</ElButton>
              </ElUpload>
              <ElButton type="danger" class="custom-height" @click="openStudentBatchDelete">批量删除</ElButton>
            </div>
          </div>

          <div v-if="importResult" style="margin-bottom:16px;padding:12px;background:#f0f9eb;border-radius:4px;font-size:14px;">
            <span style="color:#67c23a;">成功导入 {{ importResult.imported }} 条</span>
            <span v-if="importResult.errors && importResult.errors.length" style="color:#e6a23c;margin-left:16px;">{{ importResult.errors.length }} 条失败</span>
            <ul v-if="importResult.errors && importResult.errors.length" style="margin:4px 0 0;color:#909399;font-size:13px;">
              <li v-for="(err, i) in importResult.errors" :key="i">{{ err }}</li>
            </ul>
          </div>

          <div class="students-table-wrap">
            <ElTable :data="students" v-loading="studentBusy" border stripe style="width:100%">
              <ElTableColumn prop="id" label="学号" width="180" />
              <ElTableColumn prop="stu_name" label="姓名" width="180" />
              <ElTableColumn label="操作" width="180">
                <template #default="{ row }">
                  <ElButton type="primary" text size="small" @click="startEditStudent(row)">编辑</ElButton>
                  <ElButton type="danger" text size="small" @click="deleteStudent(row)">删除</ElButton>
                </template>
              </ElTableColumn>
            </ElTable>
          </div>

          <ElDialog :model-value="!!editingStudent" title="编辑学生" width="400px" @close="cancelEditStudent">
            <ElForm label-position="top" @submit.prevent="submitEditStudent">
              <ElFormItem label="学号">
                <ElInput v-model="studentEditForm.id" disabled />
              </ElFormItem>
              <ElFormItem label="姓名">
                <ElInput v-model="studentEditForm.stu_name" />
              </ElFormItem>
              <div style="display:flex;gap:10px;justify-content:flex-end;">
                <ElButton @click="cancelEditStudent">取消</ElButton>
                <ElButton type="primary" native-type="submit" :loading="studentBusy">保存</ElButton>
              </div>
            </ElForm>
          </ElDialog>
          <ElDialog v-model="studentBatchDeleteVisible" title="批量删除学生" width="760px">
            <ElForm label-position="top">
              <ElFormItem>
                <ElCheckbox v-model="studentBatchDeleteFilters.universalEnabled">字段包含</ElCheckbox>
                <ElInput v-model="studentBatchDeleteFilters.universal" :disabled="!studentBatchDeleteFilters.universalEnabled" placeholder="搜索学号或姓名" style="margin-top:8px" />
              </ElFormItem>
              <ElFormItem>
                <ElCheckbox v-model="studentBatchDeleteFilters.fieldEnabled">特定字段包含</ElCheckbox>
                <div v-for="(filter, index) in studentBatchDeleteFilters.fields" :key="index" style="display:flex;gap:10px;width:100%;margin-top:8px">
                  <ElSelect v-model="filter.field" :disabled="!studentBatchDeleteFilters.fieldEnabled" style="width:150px"><ElOption label="学号" value="id" /><ElOption label="姓名" value="stu_name" /></ElSelect>
                  <ElInput v-model="filter.value" :disabled="!studentBatchDeleteFilters.fieldEnabled" placeholder="输入包含内容" />
                  <ElButton :disabled="!studentBatchDeleteFilters.fieldEnabled || studentBatchDeleteFilters.fields.length === 1" @click="removeStudentBatchDeleteField(index)">删除</ElButton>
                </div>
                <ElButton text type="primary" :disabled="!studentBatchDeleteFilters.fieldEnabled" style="margin-top:8px" @click="addStudentBatchDeleteField">添加字段条件</ElButton>
              </ElFormItem>
            </ElForm>
            <div class="batch-delete-preview">
              <div style="display:flex;justify-content:space-between;align-items:center;margin-bottom:8px"><span>匹配 {{ studentBatchDeleteCandidates.length }} 名，已选择 {{ studentBatchDeleteSelection.length }} 名</span><ElCheckbox :model-value="studentBatchDeleteCandidates.length > 0 && studentBatchDeleteSelection.length === studentBatchDeleteCandidates.length" @change="toggleStudentBatchDeleteSelection">全选</ElCheckbox></div>
              <ElCheckboxGroup v-model="studentBatchDeleteSelection" class="batch-delete-list"><ElCheckbox v-for="student in studentBatchDeleteCandidates" :key="student.id" :value="student.id">{{ student.id }} · {{ student.stu_name }}</ElCheckbox></ElCheckboxGroup>
            </div>
            <template #footer><ElButton @click="studentBatchDeleteVisible = false">取消</ElButton><ElButton type="danger" :loading="studentBusy" @click="submitStudentBatchDelete">删除已选</ElButton></template>
          </ElDialog>
        </section>


        <section v-else-if="page === 'multi-deductions'" class="page-content art-card deductions-page-content">
          <div class="semester-toolbar">
            <h2 class="semester-title">寝室整体差扣分记录管理</h2>
            <div style="display:flex;gap:10px">
              <ElInput v-model="multiDeductionSearch" clearable placeholder="搜索 ID、日期、寝室、项目或分数" style="width:270px" />
              <ElButton type="primary" class="custom-height" @click="downloadMultiDeductionTemplate">模板下载</ElButton>
              <ElUpload :show-file-list="false" :before-upload="(file) => { importMultiDeductions(file); return false }" accept=".xlsx">
                <ElButton type="success" class="custom-height" :loading="multiDeductionBusy">导入 Excel</ElButton>
              </ElUpload>
              <ElButton type="danger" class="custom-height" @click="openMultiBatchDelete">批量删除</ElButton>
            </div>
          </div>
          <div class="deductions-table-wrap">
            <ElTable :data="filteredMultiDeductions" v-loading="multiDeductionBusy" border stripe style="width:100%">
              <ElTableColumn prop="id" label="记录ID" width="140" />
              <ElTableColumn prop="submit_date" label="日期" width="160" />
              <ElTableColumn prop="dorm_name" label="寝室名称" width="140" />
              <ElTableColumn prop="content" label="扣分项目" min-width="220" />
              <ElTableColumn prop="score" label="分数" width="90" />
              <ElTableColumn label="操作" width="400"><template #default="{ row }"><ElButton type="primary" text @click="startEditMultiDeduction(row)">编辑</ElButton><ElButton type="primary" text @click="openAppeal(row)">导出申诉模板</ElButton><ElButton type="primary" text @click="openSubrecords(row)">子项管理</ElButton><ElButton type="danger" text @click="deleteMultiDeduction(row)">删除</ElButton></template></ElTableColumn>
            </ElTable>
          </div>
          <ElDialog :model-value="!!editingMultiDeduction" title="编辑寝室整体差记录" width="500px" @close="cancelEditMultiDeduction"><ElForm label-position="top" @submit.prevent="submitEditMultiDeduction"><ElFormItem label="日期"><ElInput v-model="multiDeductionForm.submit_date" disabled /></ElFormItem><ElFormItem label="寝室名称"><ElInput v-model="multiDeductionForm.dorm_name" /></ElFormItem><ElFormItem label="扣分项目"><ElInput v-model="multiDeductionForm.content" /></ElFormItem><ElFormItem label="分数"><ElInputNumber v-model="multiDeductionForm.score" :min="0" style="width:100%" /></ElFormItem><div style="display:flex;gap:10px;justify-content:flex-end"><ElButton @click="cancelEditMultiDeduction">取消</ElButton><ElButton type="primary" native-type="submit" :loading="multiDeductionBusy">保存</ElButton></div></ElForm></ElDialog>
          <ElDialog v-model="multiBatchDeleteVisible" title="批量删除寝室整体差记录" width="760px">
            <ElForm label-position="top">
              <ElFormItem><ElCheckbox v-model="multiBatchDeleteFilters.universalEnabled">字段包含</ElCheckbox><ElInput v-model="multiBatchDeleteFilters.universal" :disabled="!multiBatchDeleteFilters.universalEnabled" placeholder="搜索 ID、日期、寝室、项目或分数" style="margin-top:8px" /></ElFormItem>
              <ElFormItem><ElCheckbox v-model="multiBatchDeleteFilters.fieldEnabled">特定字段包含</ElCheckbox><div v-for="(filter, index) in multiBatchDeleteFilters.fields" :key="index" style="display:flex;gap:10px;width:100%;margin-top:8px"><ElSelect v-model="filter.field" :disabled="!multiBatchDeleteFilters.fieldEnabled" style="width:150px"><ElOption label="ID" value="id" /><ElOption label="日期" value="submit_date" /><ElOption label="寝室名称" value="dorm_name" /><ElOption label="扣分项目" value="content" /><ElOption label="分数" value="score" /></ElSelect><ElInput v-model="filter.value" :disabled="!multiBatchDeleteFilters.fieldEnabled" placeholder="输入包含内容" /><ElButton :disabled="!multiBatchDeleteFilters.fieldEnabled || multiBatchDeleteFilters.fields.length === 1" @click="removeMultiBatchField(index)">删除</ElButton></div><ElButton text type="primary" :disabled="!multiBatchDeleteFilters.fieldEnabled" style="margin-top:8px" @click="addMultiBatchField">添加字段条件</ElButton></ElFormItem>
              <ElFormItem><ElCheckbox v-model="multiBatchDeleteFilters.dateEnabled">特定时段间隔删除</ElCheckbox><ElDatePicker v-model="multiBatchDeleteFilters.dateRange" :disabled="!multiBatchDeleteFilters.dateEnabled" type="daterange" value-format="YYYY-MM-DD" start-placeholder="开始日期" end-placeholder="结束日期" style="display:block;width:100%;margin-top:8px" /></ElFormItem>
            </ElForm>
            <div class="batch-delete-preview"><div style="display:flex;justify-content:space-between;align-items:center;margin-bottom:8px"><span>匹配 {{ multiBatchDeleteCandidates.length }} 条，已选择 {{ multiBatchDeleteSelection.length }} 条</span><ElCheckbox :model-value="multiBatchDeleteCandidates.length > 0 && multiBatchDeleteSelection.length === multiBatchDeleteCandidates.length" @change="toggleMultiBatchSelection">全选</ElCheckbox></div><ElCheckboxGroup v-model="multiBatchDeleteSelection" class="batch-delete-list"><ElCheckbox v-for="record in multiBatchDeleteCandidates" :key="record.id" :value="record.id">{{ record.submit_date }} · {{ record.dorm_name }} · {{ record.content }} · {{ record.score }}</ElCheckbox></ElCheckboxGroup></div>
            <template #footer><ElButton @click="multiBatchDeleteVisible = false">取消</ElButton><ElButton type="danger" :loading="multiDeductionBusy" @click="submitMultiBatchDelete">删除已选</ElButton></template>
          </ElDialog>
          <ElDialog :model-value="!!managingSubrecords" title="子项管理" width="760px" @close="managingSubrecords = null">
            <ElForm label-position="top" @submit.prevent="saveMultiSubrecord">
              <ElFormItem label="日期"><ElInput :model-value="managingSubrecords?.submit_date || ''" readonly /></ElFormItem>
              <ElFormItem label="扣分记录内容"><ElInput :model-value="managingSubrecords?.content || ''" readonly /></ElFormItem>
              <ElFormItem label="子项内容"><ElInput v-model="multiSubrecordForm.content" /></ElFormItem>
              <ElFormItem label="负责同学"><ElSelect v-model="multiSubrecordForm.student_ids" multiple filterable clearable placeholder="搜索学号或姓名" style="width:100%"><ElOption v-for="student in students" :key="student.id" :label="`${student.stu_name} (${student.id})`" :value="student.id" /></ElSelect></ElFormItem>
              <div style="display:flex;gap:10px;justify-content:flex-end"><ElButton @click="resetMultiSubrecordForm">取消编辑</ElButton><ElButton type="primary" native-type="submit" :loading="multiDeductionBusy">保存子项</ElButton></div>
            </ElForm>
            <ElTable :data="multiSubrecords" border stripe style="width:100%;margin-top:20px"><ElTableColumn prop="content" label="子项内容" min-width="200" /><ElTableColumn prop="student_names" label="负责同学" min-width="180" /><ElTableColumn label="操作" width="130"><template #default="{ row }"><ElButton type="primary" text @click="editMultiSubrecord(row)">编辑</ElButton><ElButton type="danger" text @click="deleteMultiSubrecord(row)">删除</ElButton></template></ElTableColumn></ElTable>
          </ElDialog>
        </section>


        <!-- ── Deduction records ── -->
        <section v-else-if="page === 'deductions'" class="page-content art-card deductions-page-content">
          <div class="semester-toolbar">
            <h2 class="semester-title">常规扣分记录管理</h2>
            <div style="display:flex;gap:10px;align-items:center;">
              <ElInput v-model="deductionSearch" clearable placeholder="搜索记录 ID、姓名、认定、日期、内容或分数" style="width:400px" />
              <ElButton type="primary" class="custom-height" @click="downloadDeductionTemplate">模板下载</ElButton>
              <ElUpload :show-file-list="false" :before-upload="(f) => { importDeductions(f); return false }" accept=".xlsx">
                <ElButton type="success" class="custom-height" :loading="deductionBusy">导入 Excel</ElButton>
              </ElUpload>
              <ElButton type="danger" class="custom-height" @click="openBatchDelete">批量删除</ElButton>
            </div>
          </div>

          <div v-if="deductionImportResult" style="margin-bottom:16px;padding:12px;background:#f0f9eb;border-radius:4px;font-size:14px;">
            <span style="color:#67c23a;">成功导入 {{ deductionImportResult.imported }} 条</span>
            <span v-if="deductionImportResult.errors && deductionImportResult.errors.length" style="color:#e6a23c;margin-left:16px;">{{ deductionImportResult.errors.length }} 条失败</span>
            <ul v-if="deductionImportResult.errors && deductionImportResult.errors.length" style="margin:4px 0 0;color:#909399;font-size:13px;">
              <li v-for="(err, i) in deductionImportResult.errors" :key="i">{{ err }}</li>
            </ul>
          </div>

          <div class="deductions-table-wrap">
            <ElTable :data="filteredDeductions" v-loading="deductionBusy" border stripe style="width:100%">
              <ElTableColumn prop="id" label="记录ID" width="120" />
              <ElTableColumn prop="student_name" label="姓名" width="120" />
              <ElTableColumn label="认定" min-width="180">
                <template #default="{ row }">
                  <span>{{ row.recognized_students || '未认定' }}</span>
                  <ElButton type="primary" text size="small" @click="startEditRecognition(row)">编辑</ElButton>
                </template>
              </ElTableColumn>
              <ElTableColumn prop="submit_date" label="日期" width="130" />
              <ElTableColumn prop="content" label="扣分内容" min-width="180" />
              <ElTableColumn prop="score" label="分数" width="80" />
              <ElTableColumn label="操作" width="270">
                <template #default="{ row }">
                  <ElButton type="primary" text size="small" @click="startEditDeduction(row)">编辑</ElButton>
                  <ElButton type="primary" text size="small" @click="openAppeal(row)">导出申诉模板</ElButton>
                  <ElButton type="danger" text size="small" @click="deleteDeduction(row)">删除</ElButton>
                </template>
              </ElTableColumn>
            </ElTable>
          </div>

          <ElDialog :model-value="!!editingDeduction" title="编辑扣分记录" width="500px" @close="cancelEditDeduction">
            <ElForm label-position="top" @submit.prevent="submitEditDeduction">
              <ElFormItem label="记录ID">
                <ElInput v-model="deductionEditForm.id" disabled />
              </ElFormItem>
              <ElFormItem label="姓名">
                <ElInput v-model="deductionEditForm.student_name" />
              </ElFormItem>
              <ElFormItem label="日期">
                <ElInput v-model="deductionEditForm.submit_date" placeholder="YYYY-MM-DD" />
              </ElFormItem>
              <ElFormItem label="扣分项目">
                <ElInput v-model="deductionEditForm.content" />
              </ElFormItem>
              <ElFormItem label="分数">
                <ElInput v-model="deductionEditForm.score" />
              </ElFormItem>
              <div style="display:flex;gap:10px;justify-content:flex-end;">
                <ElButton @click="cancelEditDeduction">取消</ElButton>
                <ElButton type="primary" native-type="submit" :loading="deductionBusy">保存</ElButton>
              </div>
            </ElForm>
          </ElDialog>

          <ElDialog :model-value="!!editingRecognition" title="编辑认定" width="500px" @close="cancelEditRecognition">
            <ElForm label-position="top" @submit.prevent="submitEditRecognition">
              <ElFormItem label="认定学生">
                <ElSelect v-model="recognizedStudentIDs" multiple filterable clearable placeholder="搜索学号或姓名" style="width:100%">
                  <ElOption v-for="student in students" :key="student.id" :label="`${student.stu_name} (${student.id})`" :value="student.id" />
                </ElSelect>
              </ElFormItem>
              <div style="display:flex;gap:10px;justify-content:flex-end;">
                <ElButton @click="cancelEditRecognition">取消</ElButton>
                <ElButton type="primary" native-type="submit" :loading="deductionBusy">保存</ElButton>
              </div>
            </ElForm>
          </ElDialog>

          <ElDialog v-model="batchDeleteVisible" title="批量删除扣分记录" width="760px">
            <ElForm label-position="top">
              <ElFormItem>
                <ElCheckbox v-model="batchDeleteFilters.universalEnabled">字段包含</ElCheckbox>
                <ElInput v-model="batchDeleteFilters.universal" :disabled="!batchDeleteFilters.universalEnabled" placeholder="搜索 ID、姓名、认定、日期、扣分内容或分数" style="margin-top:8px" />
              </ElFormItem>
              <ElFormItem>
                <ElCheckbox v-model="batchDeleteFilters.fieldEnabled">特定字段包含</ElCheckbox>
                <div v-for="(filter, index) in batchDeleteFilters.fields" :key="index" style="display:flex;gap:10px;width:100%;margin-top:8px">
                  <ElSelect v-model="filter.field" :disabled="!batchDeleteFilters.fieldEnabled" style="width:150px">
                    <ElOption label="ID" value="id" /><ElOption label="姓名" value="student_name" /><ElOption label="认定" value="recognized_students" /><ElOption label="扣分内容" value="content" /><ElOption label="分数" value="score" />
                  </ElSelect>
                  <ElInput v-model="filter.value" :disabled="!batchDeleteFilters.fieldEnabled" placeholder="输入包含内容" />
                  <ElButton :disabled="!batchDeleteFilters.fieldEnabled || batchDeleteFilters.fields.length === 1" @click="removeBatchDeleteField(index)">删除</ElButton>
                </div>
                <ElButton text type="primary" :disabled="!batchDeleteFilters.fieldEnabled" style="margin-top:8px" @click="addBatchDeleteField">添加字段条件</ElButton>
              </ElFormItem>
              <ElFormItem>
                <ElCheckbox v-model="batchDeleteFilters.dateEnabled">特定时段间隔删除</ElCheckbox>
                <ElDatePicker v-model="batchDeleteFilters.dateRange" :disabled="!batchDeleteFilters.dateEnabled" type="daterange" value-format="YYYY-MM-DD" start-placeholder="开始日期" end-placeholder="结束日期" style="display:block;width:100%;margin-top:8px" />
              </ElFormItem>
            </ElForm>
            <div class="batch-delete-preview">
              <div style="display:flex;justify-content:space-between;align-items:center;margin-bottom:8px">
                <span>匹配 {{ batchDeleteCandidates.length }} 条，已选择 {{ batchDeleteSelection.length }} 条</span>
                <ElCheckbox :model-value="batchDeleteCandidates.length > 0 && batchDeleteSelection.length === batchDeleteCandidates.length" @change="toggleBatchDeleteSelection">全选</ElCheckbox>
              </div>
              <ElCheckboxGroup v-model="batchDeleteSelection" class="batch-delete-list">
                <ElCheckbox v-for="record in batchDeleteCandidates" :key="record.id" :value="record.id">
                  {{ record.submit_date }} · {{ record.student_name }} · {{ record.recognized_students || '未认定' }} · {{ record.content }} · {{ record.score }}
                </ElCheckbox>
              </ElCheckboxGroup>
            </div>
            <template #footer>
              <ElButton @click="batchDeleteVisible = false">取消</ElButton>
              <ElButton type="danger" :loading="deductionBusy" @click="submitBatchDelete">删除已选</ElButton>
            </template>
          </ElDialog>
        </section>

<!-- ── Change password ── -->
        <ElForm v-else class="password-form page-content art-card" label-position="top" @submit.prevent="submitPassword">
          <h2>修改密码</h2>
          <ElFormItem label="旧密码"><ElInput v-model="password.old_password" type="password" show-password /></ElFormItem>
          <ElFormItem label="新密码"><ElInput v-model="password.new_password" type="password" show-password /></ElFormItem>
          <ElFormItem label="确认新密码"><ElInput v-model="password.confirm" type="password" show-password @keyup.enter="submitPassword" /></ElFormItem>
          <ElButton type="primary" :loading="busy" native-type="submit">确认修改</ElButton>
        </ElForm>
      </main>
      <SiteFooter />
    </div>
  </div>
</template>
