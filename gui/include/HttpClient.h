#pragma once

#include <string>

struct HttpResponse {
    long status = 0;
    std::string body;
};

class HttpClient {
public:
    explicit HttpClient(std::string baseUrl);
    HttpResponse postJson(const std::string& path, const std::string& json);
    HttpResponse get(const std::string& path);
    const std::string& cookie() const { return cookie_; }

private:
    HttpResponse request(const std::string& method, const std::string& path, const std::string& body);
    std::string baseUrl_;
    std::string cookie_;
};
