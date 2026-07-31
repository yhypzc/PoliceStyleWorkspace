#ifdef _WIN32
#ifndef WIN32_LEAN_AND_MEAN
#define WIN32_LEAN_AND_MEAN
#endif
#include <windows.h>
#include <GLFW/glfw3.h>
#include "../../third_party/EUI-NEO/core/window/window_backend.h"

namespace core::window {
Handle createFixedWindow(WindowCreateRequest request) {
    request.resizable = false;
    request.highDpi = false;
    return createWindow(request);
}
}

static void getFixedWindowContentScale(GLFWwindow*, float* x, float* y) {
    if (x) *x = 1.0f;
    if (y) *y = 1.0f;
}

// EUI-NEO supplies the complete GLFW/OpenGL application loop in this source.
// Rename its portable main function and invoke it from the Windows GUI entry.
// With the WIN32 CMake target, MinGW links WinMainCRTStartup, which initializes
// the CRT and calls WinMain without creating a console window.
#define main policeStyleWorkspaceEuiMain
#define createWindow createFixedWindow
#define glfwGetWindowContentScale getFixedWindowContentScale
#include "../../third_party/EUI-NEO/core/app/glfw_app_main.cpp"
#undef glfwGetWindowContentScale
#undef createWindow
#undef main

int WINAPI WinMain(HINSTANCE, HINSTANCE, LPSTR, int) {
    return policeStyleWorkspaceEuiMain();
}
#else
#error PoliceStyleWorkspace GUI is a Windows-only application.
#endif
