#include "WatchCapture.h"

#include <windows.h>

#include <string>

namespace {
std::wstring widen(const std::string& value) {
    const int size = MultiByteToWideChar(CP_UTF8, 0, value.c_str(), -1, nullptr, 0);
    std::wstring result(size > 0 ? size - 1 : 0, L'\0');
    if (size > 1) MultiByteToWideChar(CP_UTF8, 0, value.c_str(), -1, result.data(), size);
    return result;
}
}

// Adapted from Watch-On-Windows/watch.cpp::GetCommandOutput. The original
// _popen call creates a visible console from a GUI process. This version keeps
// its pipe-capture behavior but uses CreateProcess(CREATE_NO_WINDOW), so no
// command window can appear on either the UI or worker thread.
std::string captureCommandOutput(const std::string& command) {
    SECURITY_ATTRIBUTES security{sizeof(SECURITY_ATTRIBUTES), nullptr, TRUE};
    HANDLE readPipe = nullptr;
    HANDLE writePipe = nullptr;
    if (!CreatePipe(&readPipe, &writePipe, &security, 0)) return {};
    SetHandleInformation(readPipe, HANDLE_FLAG_INHERIT, 0);

    STARTUPINFOW startup{};
    PROCESS_INFORMATION process{};
    startup.cb = sizeof(startup);
    startup.dwFlags = STARTF_USESTDHANDLES | STARTF_USESHOWWINDOW;
    startup.hStdOutput = writePipe;
    startup.hStdError = writePipe;
    startup.hStdInput = GetStdHandle(STD_INPUT_HANDLE);
    startup.wShowWindow = SW_HIDE;

    std::wstring commandLine = widen(command);
    const BOOL started = CreateProcessW(nullptr, commandLine.data(), nullptr, nullptr, TRUE,
                                        CREATE_NO_WINDOW | CREATE_UNICODE_ENVIRONMENT,
                                        nullptr, nullptr, &startup, &process);
    CloseHandle(writePipe);
    if (!started) {
        CloseHandle(readPipe);
        return {};
    }

    std::string result;
    char buffer[1024];
    DWORD bytesRead = 0;
    while (ReadFile(readPipe, buffer, sizeof(buffer), &bytesRead, nullptr) && bytesRead > 0) {
        result.append(buffer, buffer + bytesRead);
    }
    WaitForSingleObject(process.hProcess, 5000);
    CloseHandle(process.hThread);
    CloseHandle(process.hProcess);
    CloseHandle(readPipe);
    return result;
}
