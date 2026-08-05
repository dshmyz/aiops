package main

import (
	"encoding/xml"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
)

const casBaseURL = "http://localhost:8443"

type ServiceResponse struct {
	XMLName               xml.Name               `xml:"serviceResponse"`
	AuthenticationSuccess *AuthenticationSuccess  `xml:"authenticationSuccess"`
	AuthenticationFailure *AuthenticationFailure  `xml:"authenticationFailure"`
}

type AuthenticationSuccess struct {
	User       string            `xml:"user"`
	Attributes map[string]string `xml:"attributes"`
}

type AuthenticationFailure struct {
	Code    string `xml:"code,attr"`
	Message string `xml:",chardata"`
}

func main() {
	fmt.Println("==========================================")
	fmt.Println("  CAS 连接测试")
	fmt.Println("==========================================")
	fmt.Println()

	fmt.Printf("检查 CAS 服务器状态: %s\n", casBaseURL)
	resp, err := http.Get(casBaseURL + "/status")
	if err != nil {
		log.Fatalf("连接 CAS 服务器失败: %v\n请先运行 ./start.sh 启动服务器", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	fmt.Printf("服务器状态: %s\n", string(body))
	fmt.Println()

	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}

	serviceURL := "http://localhost:8080/api/v1/auth/cas/callback"
	loginURL := fmt.Sprintf("%s/login?service=%s", casBaseURL, url.QueryEscape(serviceURL))

	fmt.Printf("1. 访问登录页面: %s\n", loginURL)
	resp, err = client.Get(loginURL)
	if err != nil {
		log.Fatalf("访问登录页面失败: %v", err)
	}
	defer resp.Body.Close()
	fmt.Printf("   状态码: %d\n", resp.StatusCode)
	fmt.Println()

	fmt.Println("2. 提交登录表单 (zhangsan / 123456)")
	formData := url.Values{
		"username": {"zhangsan"},
		"password": {"123456"},
		"service":  {serviceURL},
	}

	resp, err = client.PostForm(casBaseURL+"/login", formData)
	if err != nil {
		log.Fatalf("提交登录表单失败: %v", err)
	}
	defer resp.Body.Close()

	fmt.Printf("   状态码: %d\n", resp.StatusCode)
	fmt.Printf("   重定向地址: %s\n", resp.Header.Get("Location"))
	fmt.Println()

	redirectURL := resp.Header.Get("Location")
	if redirectURL == "" {
		log.Fatal("未收到重定向地址，登录可能失败")
	}

	parsedURL, err := url.Parse(redirectURL)
	if err != nil {
		log.Fatalf("解析重定向地址失败: %v", err)
	}

	ticket := parsedURL.Query().Get("ticket")
	if ticket == "" {
		log.Fatal("未获取到 CAS 票据")
	}

	fmt.Printf("3. 获取到 CAS 票据: %s\n", ticket)
	fmt.Println()

	validateURL := fmt.Sprintf("%s/serviceValidate?service=%s&ticket=%s",
		casBaseURL,
		url.QueryEscape(serviceURL),
		url.QueryEscape(ticket),
	)

	fmt.Printf("4. 验证票据: %s\n", validateURL)
	resp, err = http.Get(validateURL)
	if err != nil {
		log.Fatalf("票据验证请求失败: %v", err)
	}
	defer resp.Body.Close()

	body, _ = io.ReadAll(resp.Body)
	fmt.Printf("   响应内容:\n%s\n", string(body))
	fmt.Println()

	var casResp ServiceResponse
	if err := xml.Unmarshal(body, &casResp); err != nil {
		log.Fatalf("解析 CAS 响应失败: %v", err)
	}

	if casResp.AuthenticationFailure != nil {
		log.Fatalf("CAS 认证失败: [%s] %s",
			casResp.AuthenticationFailure.Code,
			strings.TrimSpace(casResp.AuthenticationFailure.Message))
	}

	if casResp.AuthenticationSuccess == nil {
		log.Fatal("CAS 响应格式错误: 缺少 authenticationSuccess")
	}

	fmt.Println("==========================================")
	fmt.Println("  CAS 认证成功!")
	fmt.Println("==========================================")
	fmt.Println()
	fmt.Printf("用户名: %s\n", casResp.AuthenticationSuccess.User)
	fmt.Println("属性:")
	for key, value := range casResp.AuthenticationSuccess.Attributes {
		fmt.Printf("  %s: %s\n", key, value)
	}
	fmt.Println()

	fmt.Println("5. 测试错误密码登录")
	errorFormData := url.Values{
		"username": {"zhangsan"},
		"password": {"wrongpassword"},
		"service":  {serviceURL},
	}

	resp, err = client.PostForm(casBaseURL+"/login", errorFormData)
	if err != nil {
		log.Fatalf("提交错误密码失败: %v", err)
	}
	defer resp.Body.Close()
	fmt.Printf("   状态码: %d (预期 302 重定向回登录页)\n", resp.StatusCode)
	fmt.Println()

	fmt.Println("6. 测试无效票据验证")
	invalidValidateURL := fmt.Sprintf("%s/serviceValidate?service=%s&ticket=%s",
		casBaseURL,
		url.QueryEscape(serviceURL),
		url.QueryEscape("ST-invalid-ticket-12345"),
	)

	resp, err = http.Get(invalidValidateURL)
	if err != nil {
		log.Fatalf("无效票据验证请求失败: %v", err)
	}
	defer resp.Body.Close()

	body, _ = io.ReadAll(resp.Body)
	var invalidResp ServiceResponse
	if err := xml.Unmarshal(body, &invalidResp); err != nil {
		log.Fatalf("解析无效票据响应失败: %v", err)
	}

	if invalidResp.AuthenticationFailure != nil {
		fmt.Printf("   正确返回认证失败: [%s] %s\n",
			invalidResp.AuthenticationFailure.Code,
			strings.TrimSpace(invalidResp.AuthenticationFailure.Message))
	} else {
		fmt.Println("   警告: 无效票据未返回错误")
	}

	fmt.Println()
	fmt.Println("==========================================")
	fmt.Println("  CAS 测试完成!")
	fmt.Println("==========================================")
}
