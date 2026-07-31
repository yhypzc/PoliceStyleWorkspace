#pragma once

#include <string>
#include <vector>

struct ProcessInfo {
    unsigned long pid = 0;
    std::wstring exeName;
};

std::vector<ProcessInfo> findProcessesByName(const std::wstring& exeName);
bool startDetachedProcess(const std::wstring& exePath, const std::wstring& args, const std::wstring& workDir);
bool startHiddenProcessWithOutput(const std::wstring& exePath, const std::wstring& args,
                                  const std::wstring& workDir, const std::wstring& outputPath);
bool waitForProcessesToExit(const std::wstring& exeName, unsigned long timeoutMs);
bool waitForProcessPathToExit(const std::wstring& exePath, unsigned long timeoutMs);
bool isProcessPathRunning(const std::wstring& exePath);
unsigned long findProcessIdByPath(const std::wstring& exePath);
bool terminateProcessByPath(const std::wstring& exePath);
std::wstring getExecutableDirectory();
