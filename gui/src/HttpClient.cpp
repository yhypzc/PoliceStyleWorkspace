#include "HttpClient.h"

#include <stdexcept>
#include <windows.h>
#include <wininet.h>

namespace {
std::wstring widen(const std::string& s) {
    int n = MultiByteToWideChar(CP_UTF8, 0, s.c_str(), -1, nullptr, 0);
    std::wstring out(n ? n - 1 : 0, L'\0');
    if (n > 1) MultiByteToWideChar(CP_UTF8, 0, s.c_str(), -1, out.data(), n);
    return out;
}

std::string narrow(const std::wstring& s) {
    int n = WideCharToMultiByte(CP_UTF8, 0, s.c_str(), -1, nullptr, 0, nullptr, nullptr);
    std::string out(n ? n - 1 : 0, '\0');
    if (n > 1) WideCharToMultiByte(CP_UTF8, 0, s.c_str(), -1, out.data(), n, nullptr, nullptr);
    return out;
}
}

HttpClient::HttpClient(std::string baseUrl) : baseUrl_(std::move(baseUrl)) {
    if (!baseUrl_.empty() && baseUrl_.back() == '/') baseUrl_.pop_back();
}

HttpResponse HttpClient::postJson(const std::string& path, const std::string& json) {
    return request("POST", path, json);
}

HttpResponse HttpClient::get(const std::string& path) {
    return request("GET", path, "");
}

HttpResponse HttpClient::request(const std::string& method, const std::string& path, const std::string& body) {
    std::string url = baseUrl_ + path;
    HINTERNET internet = InternetOpenW(L"PoliceStyleWorkspaceGUI", INTERNET_OPEN_TYPE_PRECONFIG, nullptr, nullptr, 0);
    if (!internet) return {0, "InternetOpen failed"};

    const DWORD timeout = 3000;
    InternetSetOptionW(internet, INTERNET_OPTION_CONNECT_TIMEOUT, (LPVOID)&timeout, sizeof(timeout));
    InternetSetOptionW(internet, INTERNET_OPTION_SEND_TIMEOUT, (LPVOID)&timeout, sizeof(timeout));
    InternetSetOptionW(internet, INTERNET_OPTION_RECEIVE_TIMEOUT, (LPVOID)&timeout, sizeof(timeout));

    URL_COMPONENTSW parts{};
    wchar_t host[256]{};
    wchar_t urlPath[1024]{};
    parts.dwStructSize = sizeof(parts);
    parts.lpszHostName = host;
    parts.dwHostNameLength = 256;
    parts.lpszUrlPath = urlPath;
    parts.dwUrlPathLength = 1024;
    std::wstring wurl = widen(url);
    if (!InternetCrackUrlW(wurl.c_str(), 0, 0, &parts)) {
        InternetCloseHandle(internet);
        return {0, "invalid server URL"};
    }

    HINTERNET connection = InternetConnectW(internet, host, parts.nPort, nullptr, nullptr, INTERNET_SERVICE_HTTP, 0, 0);
    if (!connection) {
        InternetCloseHandle(internet);
        return {0, "server connection failed"};
    }
    const DWORD flags = INTERNET_FLAG_RELOAD | INTERNET_FLAG_NO_CACHE_WRITE |
        (parts.nScheme == INTERNET_SCHEME_HTTPS ? INTERNET_FLAG_SECURE : 0);
    HINTERNET request = HttpOpenRequestW(connection, widen(method).c_str(), urlPath, nullptr, nullptr, nullptr, flags, 0);
    if (!request) {
        InternetCloseHandle(connection);
        InternetCloseHandle(internet);
        return {0, "request creation failed"};
    }
    std::wstring headers;
    if (method == "POST") headers += L"Content-Type: application/json\r\n";
    if (!cookie_.empty()) headers += L"Cookie: " + widen(cookie_) + L"\r\n";
    const BOOL sent = HttpSendRequestW(request, headers.c_str(), static_cast<DWORD>(headers.size()),
                                       body.empty() ? nullptr : (LPVOID)body.data(), static_cast<DWORD>(body.size()));
    if (!sent) {
        InternetCloseHandle(request);
        InternetCloseHandle(connection);
        InternetCloseHandle(internet);
        return {0, "HTTP request failed"};
    }

    long status = 0;
    DWORD statusSize = sizeof(status);
    HttpQueryInfoW(request, HTTP_QUERY_STATUS_CODE | HTTP_QUERY_FLAG_NUMBER, &status, &statusSize, nullptr);

    wchar_t setCookie[2048]{};
    DWORD cookieSize = sizeof(setCookie);
    if (HttpQueryInfoW(request, HTTP_QUERY_SET_COOKIE, setCookie, &cookieSize, nullptr)) {
        std::string raw = narrow(setCookie);
        auto pos = raw.find(';');
        cookie_ = raw.substr(0, pos);
    }

    std::string response;
    char buffer[4096];
    DWORD read = 0;
    while (InternetReadFile(request, buffer, sizeof(buffer), &read) && read > 0) {
        response.append(buffer, buffer + read);
    }

    InternetCloseHandle(request);
    if (connection) InternetCloseHandle(connection);
    InternetCloseHandle(internet);
    return {status, response};
}
