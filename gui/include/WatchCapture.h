#pragma once

#include <string>

// Adapted from zmyme/Watch-On-Windows watch.cpp. The upstream implementation
// repeatedly executes a command with _popen and captures its terminal output.
std::string captureCommandOutput(const std::string& command);
