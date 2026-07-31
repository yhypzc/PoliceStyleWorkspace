#include "ProcessUtils.h"

#include <windows.h>
#include <tlhelp32.h>
#include <filesystem>

namespace {
std::wstring canonicalPath(const std::wstring& path) {
    const DWORD required = GetLongPathNameW(path.c_str(), nullptr, 0);
    if (required == 0) return path;
    std::wstring result(required, L'\0');
    const DWORD written = GetLongPathNameW(path.c_str(), result.data(), required);
    if (written == 0 || written >= required) return path;
    result.resize(written);
    return result;
}

bool samePath(const std::wstring& left, const std::wstring& right) {
    return _wcsicmp(canonicalPath(left).c_str(), canonicalPath(right).c_str()) == 0;
}

unsigned long processIdByPath(const std::wstring& exePath) {
    const auto processes = findProcessesByName(std::filesystem::path(exePath).filename().wstring());
    for (const auto& process : processes) {
        HANDLE handle = OpenProcess(PROCESS_QUERY_LIMITED_INFORMATION, FALSE, process.pid);
        if (!handle) continue;
        wchar_t path[32768]{};
        DWORD length = static_cast<DWORD>(std::size(path));
        const bool matches = QueryFullProcessImageNameW(handle, 0, path, &length) &&
                             samePath(path, exePath);
        CloseHandle(handle);
        if (matches) return process.pid;
    }
    return 0;
}
}

bool isProcessPathRunning(const std::wstring& exePath) {
    return processIdByPath(exePath) != 0;
}

unsigned long findProcessIdByPath(const std::wstring& exePath) {
    return processIdByPath(exePath);
}

bool terminateProcessByPath(const std::wstring& exePath) {
    bool terminated = false;
    const auto processes = findProcessesByName(std::filesystem::path(exePath).filename().wstring());
    for (const auto& process : processes) {
        HANDLE handle = OpenProcess(PROCESS_QUERY_LIMITED_INFORMATION | PROCESS_TERMINATE, FALSE, process.pid);
        if (!handle) continue;
        wchar_t path[32768]{};
        DWORD length = static_cast<DWORD>(std::size(path));
        if (QueryFullProcessImageNameW(handle, 0, path, &length) && samePath(path, exePath)) {
            terminated = TerminateProcess(handle, 0) == TRUE || terminated;
        }
        CloseHandle(handle);
    }
    return terminated;
}

std::vector<ProcessInfo> findProcessesByName(const std::wstring& exeName) {
    std::vector<ProcessInfo> result;
    HANDLE snap = CreateToolhelp32Snapshot(TH32CS_SNAPPROCESS, 0);
    if (snap == INVALID_HANDLE_VALUE) return result;
    PROCESSENTRY32W pe{};
    pe.dwSize = sizeof(pe);
    if (Process32FirstW(snap, &pe)) {
        do {
            if (_wcsicmp(pe.szExeFile, exeName.c_str()) == 0) {
                result.push_back({pe.th32ProcessID, pe.szExeFile});
            }
        } while (Process32NextW(snap, &pe));
    }
    CloseHandle(snap);
    return result;
}

bool startDetachedProcess(const std::wstring& exePath, const std::wstring& args, const std::wstring& workDir) {
    std::wstring cmd = L"\"" + exePath + L"\" " + args;
    STARTUPINFOW si{};
    PROCESS_INFORMATION pi{};
    si.cb = sizeof(si);
    si.dwFlags = STARTF_USESHOWWINDOW;
    si.wShowWindow = SW_HIDE;
    BOOL ok = CreateProcessW(nullptr, cmd.data(), nullptr, nullptr, FALSE,
                             CREATE_NO_WINDOW | CREATE_UNICODE_ENVIRONMENT,
                             nullptr, workDir.c_str(), &si, &pi);
    if (ok) {
        CloseHandle(pi.hThread);
        CloseHandle(pi.hProcess);
    }
    return ok == TRUE;
}

bool startHiddenProcessWithOutput(const std::wstring& exePath, const std::wstring& args,
                                  const std::wstring& workDir, const std::wstring& outputPath) {
    SECURITY_ATTRIBUTES security{sizeof(SECURITY_ATTRIBUTES), nullptr, TRUE};
    HANDLE output = CreateFileW(outputPath.c_str(), FILE_APPEND_DATA,
                                FILE_SHARE_READ | FILE_SHARE_WRITE, &security,
                                OPEN_ALWAYS, FILE_ATTRIBUTE_NORMAL, nullptr);
    if (output == INVALID_HANDLE_VALUE) return false;

    std::wstring command = L"\"" + exePath + L"\" " + args;
    STARTUPINFOW startup{};
    PROCESS_INFORMATION process{};
    startup.cb = sizeof(startup);
    startup.dwFlags = STARTF_USESTDHANDLES | STARTF_USESHOWWINDOW;
    startup.hStdOutput = output;
    startup.hStdError = output;
    startup.hStdInput = GetStdHandle(STD_INPUT_HANDLE);
    startup.wShowWindow = SW_HIDE;
    const BOOL started = CreateProcessW(nullptr, command.data(), nullptr, nullptr, TRUE,
                                        CREATE_NO_WINDOW | CREATE_UNICODE_ENVIRONMENT,
                                        nullptr, workDir.c_str(), &startup, &process);
    CloseHandle(output);
    if (!started) return false;
    CloseHandle(process.hThread);
    CloseHandle(process.hProcess);
    return true;
}

bool waitForProcessesToExit(const std::wstring& exeName, unsigned long timeoutMs) {
    const ULONGLONG deadline = GetTickCount64() + timeoutMs;
    while (GetTickCount64() < deadline) {
        if (findProcessesByName(exeName).empty()) return true;
        Sleep(100);
    }
    return findProcessesByName(exeName).empty();
}

bool waitForProcessPathToExit(const std::wstring& exePath, unsigned long timeoutMs) {
    const ULONGLONG deadline = GetTickCount64() + timeoutMs;
    while (GetTickCount64() < deadline) {
        if (!isProcessPathRunning(exePath)) return true;
        Sleep(100);
    }
    return !isProcessPathRunning(exePath);
}

std::wstring getExecutableDirectory() {
    wchar_t path[MAX_PATH]{};
    GetModuleFileNameW(nullptr, path, MAX_PATH);
    std::wstring p(path);
    auto pos = p.find_last_of(L"\\/");
    return pos == std::wstring::npos ? L"." : p.substr(0, pos);
}
