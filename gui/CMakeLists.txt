cmake_minimum_required(VERSION 3.20)
project(PoliceStyleWorkspaceGUI LANGUAGES C CXX)

set(CMAKE_CXX_STANDARD 17)
set(CMAKE_CXX_STANDARD_REQUIRED ON)

set(EUI_BUILD_APPS OFF CACHE BOOL "" FORCE)
set(EUI_BUILD_USER_APPS OFF CACHE BOOL "" FORCE)
set(EUI_BUILD_TEST_FIXTURES OFF CACHE BOOL "" FORCE)
set(EUI_ENABLE_INSTALL OFF CACHE BOOL "" FORCE)
set(EUI_ENABLE_MODULES OFF CACHE BOOL "" FORCE)
set(EUI_ENABLE_MARKDOWN OFF CACHE BOOL "" FORCE)
set(EUI_DEPS_MODE bundled CACHE STRING "" FORCE)

set(CMAKE_C_FLAGS_DEBUG "-O0 -g")
set(CMAKE_CXX_FLAGS_DEBUG "-O0 -g")

add_subdirectory(${CMAKE_CURRENT_SOURCE_DIR}/../third_party/EUI-NEO ${CMAKE_BINARY_DIR}/eui-neo)

add_executable(police-style-workspace-gui WIN32
    src/resource.rc
    src/WinMain.cpp
    src/main.cpp
    src/HttpClient.cpp
    src/ProcessUtils.cpp
    src/TaskScheduler.cpp
    src/WatchCapture.cpp
)

target_include_directories(police-style-workspace-gui PRIVATE include)
target_link_libraries(police-style-workspace-gui PRIVATE wininet shell32 advapi32 user32 iphlpapi)
if(MINGW)
    # MinGW 11's Windows SDK predates this flag used by EUI-NEO frame pacing.
    target_compile_definitions(police-style-workspace-gui PRIVATE CREATE_WAITABLE_TIMER_HIGH_RESOLUTION=0x00000002)
endif()
eui_neo_configure_app(police-style-workspace-gui)

if (MINGW)
    target_link_options(police-style-workspace-gui PRIVATE
        -mwindows
        -Wl,-e,WinMainCRTStartup
        -static
        -static-libgcc
        -static-libstdc++
    )
endif()
