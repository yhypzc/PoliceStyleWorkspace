#include "eui_neo.h"

#include "ProcessUtils.h"
#include "TaskScheduler.h"

#include <windows.h>
#include <iphlpapi.h>
#include <shellapi.h>

#include <algorithm>
#include <atomic>
#include <chrono>
#include <condition_variable>
#include <cstdio>
#include <deque>
#include <filesystem>
#include <mutex>
#include <sstream>
#include <string>
#include <thread>

namespace app {

const DslAppConfig& dslAppConfig() {
    static const DslAppConfig config = DslAppConfig{}
        .title("纪检工作台 · 服务器管理")
        .pageId("police_style_workspace")
        .clearColor({0.945f, 0.953f, 0.965f, 1.0f})
        .windowSize(1360, 940)
        .iconPath("")
        .fps(60.0);
    return config;
}

namespace {

constexpr wchar_t kServerName[] = L"police-style-workspace-server.exe";
constexpr wchar_t kTaskName[] = L"PoliceStyleWorkspaceServerWatchdog";
constexpr wchar_t kTerminalPipe[] = L"\\\\.\\pipe\\PoliceStyleWorkspace-Terminal";
constexpr char kBrowserUrl[] = "http://localhost:3456";

enum class JobType { Poll, Search, Start, Restart, OpenBrowser };

struct WorkerResult {
    bool pending = false;
    bool running = false;
    bool processChanged = false;
    unsigned long pid = 0;
    float cpu = 0.0f;
    float disk = 0.0f;
    float memory = 0.0f;
    unsigned long long memoryUsedMb = 0;
    unsigned long long netBytes = 0;
    std::string status;
};

struct State {
    eui::Signal<std::string> terminal{"正在搜索 police-style-workspace-server.exe ...\n"};
    bool serverRunning = false;
    bool busy = false;
    bool paused = false;
    unsigned long serverPid = 0;
    float cpu = 0.0f;
    float disk = 0.0f;
    float memory = 0.0f;
    float networkRate = 0.0f;
    unsigned long long memoryUsedMb = 0;
    unsigned long long previousNetBytes = 0;
    std::chrono::steady_clock::time_point previousNetAt{};
    std::deque<float> networkHistory = std::deque<float>(24, 0.04f);
    std::string status = "正在自动搜索服务器进程";
    unsigned long terminalRevision = 0;
    std::chrono::steady_clock::time_point nextPoll{};
};

State state;
std::mutex queueMutex;
std::condition_variable queueChanged;
std::deque<JobType> jobs;
std::mutex resultMutex;
WorkerResult result;
std::mutex terminalMutex;
std::string terminalIncoming;
std::atomic<bool> workerRunning{true};

std::filesystem::path binDir() { return std::filesystem::path(getExecutableDirectory()); }
std::filesystem::path rootDir() {
    const auto bin = binDir();
    return bin.filename() == L"bin" ? bin.parent_path() : bin;
}
std::filesystem::path serverPath() { return binDir() / kServerName; }
std::filesystem::path watchdogScriptPath() { return rootDir() / L"scripts" / L"server-watchdog.vbs"; }

bool initializeWatchdogTask() {
    static bool initialized = false;
    static bool ready = false;
    if (initialized) return ready;

    ready = ensureServerWatchdogTask(kTaskName, watchdogScriptPath().wstring());
    initialized = true;
    return ready;
}

unsigned long long fileTimeValue(const FILETIME& value) {
    ULARGE_INTEGER resultValue{};
    resultValue.LowPart = value.dwLowDateTime;
    resultValue.HighPart = value.dwHighDateTime;
    return resultValue.QuadPart;
}

float currentCpuPercent() {
    static unsigned long long previousIdle = 0;
    static unsigned long long previousKernel = 0;
    static unsigned long long previousUser = 0;
    FILETIME idle{}, kernel{}, user{};
    if (!GetSystemTimes(&idle, &kernel, &user)) return 0.0f;
    const auto idleNow = fileTimeValue(idle);
    const auto kernelNow = fileTimeValue(kernel);
    const auto userNow = fileTimeValue(user);
    const auto idleDelta = idleNow - previousIdle;
    const auto totalDelta = (kernelNow - previousKernel) + (userNow - previousUser);
    previousIdle = idleNow;
    previousKernel = kernelNow;
    previousUser = userNow;
    if (totalDelta == 0) return 0.0f;
    return std::clamp(1.0f - static_cast<float>(idleDelta) / static_cast<float>(totalDelta), 0.0f, 1.0f);
}

unsigned long long currentNetworkBytes() {
    ULONG size = 0;
    if (GetIfTable(nullptr, &size, FALSE) != ERROR_INSUFFICIENT_BUFFER || size == 0) return 0;
    std::vector<unsigned char> storage(size);
    auto* table = reinterpret_cast<PMIB_IFTABLE>(storage.data());
    if (GetIfTable(table, &size, FALSE) != NO_ERROR) return 0;
    unsigned long long total = 0;
    for (DWORD index = 0; index < table->dwNumEntries; ++index) {
        const auto& row = table->table[index];
        if (row.dwType != IF_TYPE_SOFTWARE_LOOPBACK) total += row.dwInOctets + row.dwOutOctets;
    }
    return total;
}

void collectSystemStatus(WorkerResult& update) {
    update.cpu = currentCpuPercent();
    MEMORYSTATUSEX memory{};
    memory.dwLength = sizeof(memory);
    if (GlobalMemoryStatusEx(&memory)) {
        update.memory = static_cast<float>(memory.dwMemoryLoad) / 100.0f;
        update.memoryUsedMb = (memory.ullTotalPhys - memory.ullAvailPhys) / 1024 / 1024;
    }
    ULARGE_INTEGER available{}, total{}, free{};
    if (GetDiskFreeSpaceExW(L"C:\\", &available, &total, &free) && total.QuadPart > 0) {
        update.disk = 1.0f - static_cast<float>(free.QuadPart) / static_cast<float>(total.QuadPart);
    }
    update.netBytes = currentNetworkBytes();
}

void appendTerminal(std::string chunk) {
    {
        std::lock_guard<std::mutex> lock(terminalMutex);
        terminalIncoming += std::move(chunk);
        constexpr std::size_t maxPending = 256 * 1024;
        if (terminalIncoming.size() > maxPending) {
            terminalIncoming.erase(0, terminalIncoming.size() - maxPending);
        }
    }
    app::requestUpdate();
}

void terminalPipeLoop() {
    while (workerRunning.load()) {
        HANDLE pipe = CreateFileW(kTerminalPipe, GENERIC_READ, 0, nullptr, OPEN_EXISTING,
                                  FILE_FLAG_OVERLAPPED, nullptr);
        if (pipe == INVALID_HANDLE_VALUE) {
            std::this_thread::sleep_for(std::chrono::milliseconds(250));
            continue;
        }
        appendTerminal("[GUI] 已接入服务器实时终端输出。\n");
        while (workerRunning.load()) {
            char buffer[4096];
            OVERLAPPED overlapped{};
            overlapped.hEvent = CreateEventW(nullptr, TRUE, FALSE, nullptr);
            DWORD bytesRead = 0;
            const BOOL started = ReadFile(pipe, buffer, sizeof(buffer), &bytesRead, &overlapped);
            if (!started && GetLastError() != ERROR_IO_PENDING) {
                CloseHandle(overlapped.hEvent);
                break;
            }
            while (!started && workerRunning.load()) {
                const DWORD wait = WaitForSingleObject(overlapped.hEvent, 250);
                if (wait == WAIT_OBJECT_0) break;
                if (wait != WAIT_TIMEOUT) break;
            }
            if (!workerRunning.load()) CancelIoEx(pipe, &overlapped);
            const BOOL completed = started || GetOverlappedResult(pipe, &overlapped, &bytesRead, FALSE);
            CloseHandle(overlapped.hEvent);
            if (!completed || bytesRead == 0) break;
            appendTerminal(std::string(buffer, buffer + bytesRead));
        }
        CloseHandle(pipe);
        if (workerRunning.load()) {
            appendTerminal("[GUI] 实时输出连接已断开，正在重新连接...\n");
        }
    }
}

void publish(WorkerResult update) {
    {
        std::lock_guard<std::mutex> lock(resultMutex);
        update.pending = true;
        result = std::move(update);
    }
    app::requestUpdate();
}

void submit(JobType type) {
    {
        std::lock_guard<std::mutex> lock(queueMutex);
        if (type == JobType::Poll) {
            const bool queued = std::find(jobs.begin(), jobs.end(), JobType::Poll) != jobs.end();
            if (queued) return;
        }
        jobs.push_back(type);
    }
    if (type != JobType::Poll) {
        state.busy = true;
        state.status = "正在处理，请稍候...";
    }
    queueChanged.notify_one();
}

void inspectServer(WorkerResult& update) {
    update.pid = findProcessIdByPath(serverPath().wstring());
    update.running = update.pid != 0;
    update.processChanged = true;
    const bool watchdogReady = initializeWatchdogTask();
    update.status = update.running
        ? "服务器正在运行，PID " + std::to_string(update.pid)
        : watchdogReady ? "未检测到服务器，监控任务将在 1 分钟后恢复"
                        : "未检测到服务器，可以启动";
    collectSystemStatus(update);
}

void workerLoop() {
    while (workerRunning.load()) {
        JobType job = JobType::Poll;
        {
            std::unique_lock<std::mutex> lock(queueMutex);
            queueChanged.wait(lock, [] { return !jobs.empty() || !workerRunning.load(); });
            if (!workerRunning.load()) break;
            job = jobs.front();
            jobs.pop_front();
        }
        WorkerResult update;
        if (job == JobType::Start) {
            std::filesystem::create_directories(rootDir() / L"log");
            const bool alreadyRunning = isProcessPathRunning(serverPath().wstring());
            const bool started = alreadyRunning || startDetachedProcess(
                serverPath().wstring(), L"", binDir().wstring());
            std::this_thread::sleep_for(std::chrono::milliseconds(600));
            inspectServer(update);
            if (!started) update.status = "服务器启动失败，请检查可执行文件";
        } else if (job == JobType::Restart) {
            // Without credentials, terminate only the exact release process.
            terminateProcessByPath(serverPath().wstring());
            waitForProcessPathToExit(serverPath().wstring(), 5000);
            const bool started = startDetachedProcess(serverPath().wstring(), L"", binDir().wstring());
            std::this_thread::sleep_for(std::chrono::milliseconds(600));
            inspectServer(update);
            if (!started) update.status = "服务器重启失败";
        } else if (job == JobType::OpenBrowser) {
            ShellExecuteA(nullptr, "open", kBrowserUrl, nullptr, nullptr, SW_SHOWNORMAL);
            inspectServer(update);
            update.status = "已打开工作台网页";
        } else {
            inspectServer(update);
        }
        publish(std::move(update));
    }
}

void applyWorkerResult() {
    WorkerResult update;
    {
        std::lock_guard<std::mutex> lock(resultMutex);
        if (!result.pending) return;
        update = std::move(result);
        result = WorkerResult{};
    }
    state.busy = false;
    state.serverRunning = update.running;
    state.serverPid = update.pid;
    state.cpu = update.cpu;
    state.disk = update.disk;
    state.memory = update.memory;
    state.memoryUsedMb = update.memoryUsedMb;
    state.status = update.status;
    const auto now = std::chrono::steady_clock::now();
    if (state.previousNetBytes > 0 && update.netBytes >= state.previousNetBytes) {
        const float seconds = std::chrono::duration<float>(now - state.previousNetAt).count();
        if (seconds > 0.0f) state.networkRate = static_cast<float>(update.netBytes - state.previousNetBytes) / seconds;
    }
    state.previousNetBytes = update.netBytes;
    state.previousNetAt = now;
    state.networkHistory.push_back(std::clamp(state.networkRate / (20.0f * 1024.0f * 1024.0f), 0.04f, 1.0f));
    while (state.networkHistory.size() > 24) state.networkHistory.pop_front();
}

void applyTerminalIncoming() {
    std::string incoming;
    {
        std::lock_guard<std::mutex> lock(terminalMutex);
        incoming.swap(terminalIncoming);
    }
    if (!incoming.empty()) {
        std::string value = state.terminal.get();
        value += incoming;
        constexpr std::size_t maxTerminal = 512 * 1024;
        if (value.size() > maxTerminal) value.erase(0, value.size() - maxTerminal);
        state.terminal.set(std::move(value));
        if (!state.paused) ++state.terminalRevision;
    }
}

void schedulePoll() {
    const auto now = std::chrono::steady_clock::now();
    if (state.nextPoll > now) return;
    state.nextPoll = now + std::chrono::seconds(1);
    submit(JobType::Poll);
}

components::theme::ThemeColorTokens theme() {
    auto t = components::theme::light();
    t.primary = {0.08f, 0.72f, 0.49f, 1.0f};
    t.background = {0.945f, 0.953f, 0.965f, 1.0f};
    t.surface = {0.985f, 0.988f, 0.994f, 1.0f};
    t.text = {0.0f, 0.0f, 0.0f, 1.0f};
    return t;
}

void text(eui::Ui& ui, const std::string& id, const std::string& value,
          float x, float y, float width, float size = 18.0f,
          eui::Color color = {0.0f, 0.0f, 0.0f, 1.0f}) {
    ui.text(id).position(x, y).size(width, size + 12.0f).text(value).fontSize(size)
        .lineHeight(size + 5.0f).color(color).build();
}

void button(eui::Ui& ui, const std::string& id, const std::string& label,
            float x, float y, float width, bool disabled, const std::function<void()>& action, bool primary = true) {
    components::button(ui, id).position(x, y).size(width, 50).text(label).fontSize(20)
        .theme(theme(), primary).textColor({0.0f, 0.0f, 0.0f, 1.0f}).radius(7)
        .disabled(disabled || state.busy).onClick(action).build();
}

void metricCard(eui::Ui& ui, const std::string& id, const std::string& label,
                const std::string& value, float progress, float x, float y, float width) {
    components::panel(ui, id + ".panel", theme()).position(x, y).size(width, 130).radius(8).build();
    text(ui, id + ".label", label, x + 22, y + 15, width - 44, 38);
    text(ui, id + ".value", value, x + 22, y + 50, width - 44, 36);
    ui.stack(id + ".progress.wrap").position(x + 22, y + 108).size(width - 44, 11).content([&] {
        components::progress(ui, id + ".progress").size(width - 44, 11).value(progress).theme(theme()).build();
    }).build();
}

std::string rateText(float bytesPerSecond) {
    char buffer[64]{};
    if (bytesPerSecond >= 1024.0f * 1024.0f) std::snprintf(buffer, sizeof(buffer), "%.1f MB/s", bytesPerSecond / 1048576.0f);
    else std::snprintf(buffer, sizeof(buffer), "%.1f KB/s", bytesPerSecond / 1024.0f);
    return buffer;
}

void networkCard(eui::Ui& ui, float x, float y, float width) {
    components::panel(ui, "network.panel", theme()).position(x, y).size(width, 130).radius(8).build();
    text(ui, "network.label", "网络", x + 22, y + 15, width - 44, 38);
    text(ui, "network.value", rateText(state.networkRate), x + 22, y + 50, width - 44, 36);
    const float barWidth = 8.0f, gap = 5.0f;
    const float startX = x + width - 22.0f - static_cast<float>(state.networkHistory.size()) * (barWidth + gap);
    std::size_t index = 0;
    for (const float value : state.networkHistory) {
        const float height = 9.0f + value * 27.0f;
        ui.rect("network.bar." + std::to_string(index)).position(startX + index * (barWidth + gap), y + 115 - height)
            .size(barWidth, height).color(theme().primary).radius(3).build();
        ++index;
    }
}

} // namespace

void compose(eui::Ui& ui, const eui::Screen& screen) {
    static int frameCount = 0;
    ++frameCount;
    if (frameCount <= 180) {
        struct Ctx { DWORD pid; HWND hwnd; };
        Ctx ctx{GetCurrentProcessId(), nullptr};
        EnumWindows([](HWND h, LPARAM lp) -> BOOL {
            auto* c = reinterpret_cast<Ctx*>(lp);
            DWORD wp = 0; GetWindowThreadProcessId(h, &wp);
            if (wp == c->pid && IsWindowVisible(h)) {
                c->hwnd = h; return FALSE;
            }
            return TRUE;
        }, reinterpret_cast<LPARAM>(&ctx));
        if (ctx.hwnd) {
            HICON hIcon = LoadIconW(GetModuleHandleW(nullptr), L"APP_ICON");
            if (hIcon) {
                SendMessageW(ctx.hwnd, WM_SETICON, ICON_BIG, (LPARAM)hIcon);
                SendMessageW(ctx.hwnd, WM_SETICON, ICON_SMALL, (LPARAM)hIcon);
            }
        }
    }
    applyWorkerResult();
    applyTerminalIncoming();
    schedulePoll();
    ui.rect("background").size(screen.width, screen.height).color(theme().background).build();
    constexpr float pad = 30.0f;
    constexpr float contentWidth = 1300.0f;

    text(ui, "title", "纪检工作台 · 服务器管理", pad, 18, 780, 40);
    text(ui, "process", state.status, pad, 68, 700, 20, eui::Color{0.0f, 0.0f, 0.0f, 1.0f});
    button(ui, "start", state.serverRunning ? "服务器运行中" : "启动服务器", 820, 25, 160,
           state.serverRunning, [] { submit(JobType::Start); });
    button(ui, "restart", "重启服务器", 994, 25, 150, !state.serverRunning, [] { submit(JobType::Restart); });
    button(ui, "browser", "打开浏览器", 1158, 25, 150, false, [] { submit(JobType::OpenBrowser); }, false);

    text(ui, "status.title", "系统状态", pad, 106, contentWidth, 30);
    constexpr float gap = 20.0f;
    constexpr float cardWidth = (contentWidth - gap) * 0.5f;
    metricCard(ui, "cpu", "CPU", std::to_string(static_cast<int>(state.cpu * 100)) + "%", state.cpu, pad, 150, cardWidth);
    metricCard(ui, "disk", "磁盘", std::to_string(static_cast<int>(state.disk * 100)) + "%", state.disk, pad + cardWidth + gap, 150, cardWidth);
    metricCard(ui, "memory", "内存", std::to_string(static_cast<int>(state.memory * 100)) + "%  ·  " +
               std::to_string(state.memoryUsedMb) + " MB", state.memory, pad, 300, cardWidth);
    networkCard(ui, pad + cardWidth + gap, 300, cardWidth);

    text(ui, "terminal.title", "服务器日志", pad, 451, 500, 28);
    button(ui, "clear", "清空显示", 860, 446, 130, false, [] {
        state.terminal.set(""); ++state.terminalRevision;
    }, false);
    button(ui, "pause", state.paused ? "继续滚动" : "暂停滚动", 1004, 446, 130, false,
           [] { state.paused = !state.paused; if (!state.paused) ++state.terminalRevision; }, false);
    button(ui, "search", "立即搜索", 1148, 446, 130, false, [] { submit(JobType::Search); }, false);

    constexpr float terminalTop = 510.0f;
    constexpr float terminalHeight = 370.0f;
    auto terminalStyle = components::InputStyle(theme());
    terminalStyle.background = {0.015f, 0.015f, 0.015f, 1.0f};
    terminalStyle.focused = terminalStyle.background;
    terminalStyle.text = {0.94f, 0.98f, 0.95f, 1.0f};
    terminalStyle.placeholder = {0.68f, 0.72f, 0.70f, 1.0f};
    terminalStyle.border = {0.18f, 0.18f, 0.18f, 1.0f};
    terminalStyle.focusBorder = theme().primary;
    terminalStyle.cursor = theme().primary;
    terminalStyle.radius = 6.0f;
    components::input(ui, "terminal.output").position(pad, terminalTop).size(contentWidth, terminalHeight)
        .value(state.terminal.get()).placeholder("等待服务器实时输出...").multiline(true).readOnly(true)
        .scrollToEnd(state.paused ? 0 : state.terminalRevision).fontSize(20).fontFamily("monospace")
        .inset(18).style(terminalStyle).build();
    constexpr eui::Color footerTextColor{0.0f, 0.0f, 0.0f, 1.0f};
    constexpr eui::Color footerLinkColor{0.04f, 0.32f, 0.68f, 1.0f};
    constexpr float footerFontSize = 25.0f;
    constexpr float footerHeight = 30.0f;
    const std::string footerParts[] = {"·--- 作者 ", "yhypzc", " - ", "个人博客", " ---·"};
    float footerWidths[5]{};
    float footerTotalWidth = 0.0f;
    for (std::size_t i = 0; i < 5; ++i) {
        footerWidths[i] = core::TextPrimitive::measureTextWidth(
            footerParts[i], "monospace", footerFontSize, 400);
        footerTotalWidth += footerWidths[i];
    }

    ui.stack("footer")
        .position(pad, 902)
        .size(contentWidth, footerHeight)
        .content([&] {
            float x = (contentWidth - footerTotalWidth) * 0.5f;
            const auto footerText = [&](const std::string& id, std::size_t index, eui::Color color) {
                auto part = ui.text(id).position(x, 0).size(footerWidths[index], footerHeight)
                    .text(footerParts[index]).fontFamily("monospace").fontSize(footerFontSize).lineHeight(24)
                    .color(color).verticalAlign(eui::VerticalAlign::Center);
                x += footerWidths[index];
                return part;
            };
            footerText("footer.prefix", 0, footerTextColor).build();
            footerText("footer.repository", 1, footerLinkColor)
                .onClick([] {
                    ShellExecuteA(nullptr, "open", "https://github.com/yhypzc/PoliceStyleWorkspace", nullptr, nullptr, SW_SHOWNORMAL);
                })
                .build();
            footerText("footer.separator", 2, footerTextColor).build();
            footerText("footer.blog", 3, footerLinkColor)
                .onClick([] {
                    ShellExecuteA(nullptr, "open", "https://yhypzc.github.io/", nullptr, nullptr, SW_SHOWNORMAL);
                })
                .build();
            footerText("footer.suffix", 4, footerTextColor).build();
        })
        .build();
}

struct Initializer {
    std::thread worker;
    std::thread terminalPipe;
    Initializer() {
        worker = std::thread(workerLoop);
        terminalPipe = std::thread(terminalPipeLoop);
        submit(JobType::Search);
    }
    ~Initializer() {
        workerRunning.store(false);
        queueChanged.notify_all();
        if (worker.joinable()) worker.join();
        if (terminalPipe.joinable()) terminalPipe.join();
    }
} initializer;

} // namespace app
