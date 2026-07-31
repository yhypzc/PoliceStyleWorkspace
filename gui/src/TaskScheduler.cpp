#include "TaskScheduler.h"

#include <windows.h>

#include <filesystem>
#include <fstream>
#include <ctime>
#include <iomanip>
#include <sstream>
#include <string>

namespace {
bool run(const std::wstring& command, unsigned long timeoutMs = 15000) {
    STARTUPINFOW startup{};
    PROCESS_INFORMATION process{};
    startup.cb = sizeof(startup);
    startup.dwFlags = STARTF_USESHOWWINDOW;
    startup.wShowWindow = SW_HIDE;
    std::wstring mutableCommand = command;
    const BOOL started = CreateProcessW(nullptr, mutableCommand.data(), nullptr, nullptr, FALSE,
                                        CREATE_NO_WINDOW | CREATE_UNICODE_ENVIRONMENT,
                                        nullptr, nullptr, &startup, &process);
    if (!started) return false;
    WaitForSingleObject(process.hProcess, timeoutMs);
    DWORD exitCode = 1;
    GetExitCodeProcess(process.hProcess, &exitCode);
    CloseHandle(process.hThread);
    CloseHandle(process.hProcess);
    return exitCode == 0;
}

bool writeWatchdogScript(const std::wstring& scriptPath) {
    std::error_code error;
    std::filesystem::create_directories(std::filesystem::path(scriptPath).parent_path(), error);
    std::ofstream output(std::filesystem::path(scriptPath), std::ios::binary | std::ios::trunc);
    if (!output) return false;

    output << "Option Explicit\r\n"
           << "Const WatchIntervalMs = 1000\r\n"
           << "Const RestartDelayMs = 60000\r\n"
           << "Dim serverPath, serverKey, serverName, shell, wmi, fileSystem, scriptDir, rootDir\r\n"
           << "Set fileSystem = CreateObject(\"Scripting.FileSystemObject\")\r\n"
           << "scriptDir = fileSystem.GetParentFolderName(WScript.ScriptFullName)\r\n"
           << "rootDir = fileSystem.GetParentFolderName(scriptDir)\r\n"
           << "serverPath = fileSystem.BuildPath(fileSystem.BuildPath(rootDir, \"bin\"), \"police-style-workspace-server.exe\")\r\n"
           << "serverKey = LCase(fileSystem.GetFile(serverPath).ShortPath)\r\n"
           << "serverName = \"police-style-workspace-server.exe\"\r\n"
           << "Set shell = CreateObject(\"WScript.Shell\")\r\n"
           << "Set wmi = GetObject(\"winmgmts:{impersonationLevel=impersonate}!\\\\.\\root\\cimv2\")\r\n"
           << "Function ServerRunning()\r\n"
           << "  Dim processes, process\r\n"
           << "  ServerRunning = False\r\n"
           << "  Set processes = wmi.ExecQuery(\"SELECT ExecutablePath FROM Win32_Process WHERE Name='\" & serverName & \"'\")\r\n"
           << "  For Each process In processes\r\n"
           << "    If Not IsNull(process.ExecutablePath) Then\r\n"
           << "      If LCase(fileSystem.GetFile(process.ExecutablePath).ShortPath) = serverKey Then ServerRunning = True: Exit Function\r\n"
           << "    End If\r\n"
           << "  Next\r\n"
           << "End Function\r\n"
           << "Do\r\n"
           << "  If Not ServerRunning() Then\r\n"
           << "    WScript.Sleep RestartDelayMs\r\n"
           << "    If Not ServerRunning() Then shell.Run Chr(34) & serverPath & Chr(34), 0, False\r\n"
           << "  End If\r\n"
           << "  WScript.Sleep WatchIntervalMs\r\n"
           << "Loop\r\n";
    return output.good();
}
}

bool taskExists(const std::wstring& taskName) {
    return run(L"schtasks.exe /Query /TN \"" + taskName + L"\"");
}

bool ensureServerWatchdogTask(const std::wstring& taskName, const std::wstring& scriptPath) {
    const bool existingTask = taskExists(taskName);
    if (!writeWatchdogScript(scriptPath)) return false;

    if (!existingTask) {
        wchar_t shortPath[MAX_PATH]{};
        const DWORD shortLength = GetShortPathNameW(scriptPath.c_str(), shortPath, MAX_PATH);
        const std::wstring taskScript = shortLength > 0 && shortLength < MAX_PATH
            ? std::wstring(shortPath) : scriptPath;
        const std::wstring action = L"wscript.exe //B //Nologo " + taskScript;
        std::time_t start = std::time(nullptr) + 60;
        std::tm local{};
        if (localtime_s(&local, &start) != 0) return false;
        std::wostringstream startTime;
        startTime << std::setfill(L'0') << std::setw(2) << local.tm_hour
                  << L":" << std::setw(2) << local.tm_min;
        if (!run(L"schtasks.exe /Create /F /SC MINUTE /MO 1 /TN \"" + taskName +
                 L"\" /TR \"" + action + L"\" /ST " + startTime.str() + L" /RL LIMITED")) {
            return false;
        }
    }

    // /Run is harmless when the task's single watchdog instance is already active.
    run(L"schtasks.exe /Run /TN \"" + taskName + L"\"");
    return true;
}

bool deleteScheduledTask(const std::wstring& taskName) {
    if (!taskExists(taskName)) return true;
    run(L"schtasks.exe /End /TN \"" + taskName + L"\"");
    return run(L"schtasks.exe /Delete /F /TN \"" + taskName + L"\"");
}
