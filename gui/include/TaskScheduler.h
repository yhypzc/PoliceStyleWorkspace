#pragma once

#include <string>

bool taskExists(const std::wstring& taskName);
bool ensureServerWatchdogTask(const std::wstring& taskName, const std::wstring& scriptPath);
bool deleteScheduledTask(const std::wstring& taskName);
