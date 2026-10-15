package app

import (
	"testing"
)

// TestVerifyURL 验证从需要验证的上游错误正文中取出解码后的 validation_url
func TestVerifyURL(t *testing.T) {
	message := `需要验证：{"error":{"code":403,"message":"Verify your account to continue.","status":"PERMISSION_DENIED","details":[{"@type":"type.googleapis.com/google.rpc.ErrorInfo","reason":"VALIDATION_REQUIRED","domain":"cloudcode-pa.googleapis.com","metadata":{"validation_url":"https://accounts.google.com/signin/continue?sarp=1\u0026continue=https%3A%2F%2Fexample"}}]}}`
	if got := verifyURL(message); got != "https://accounts.google.com/signin/continue?sarp=1&continue=https%3A%2F%2Fexample" {
		t.Fatalf("verify_url=%q", got)
	}
	if got := verifyURL("刷新 token: 401"); got != "" {
		t.Fatalf("verify_url=%q", got)
	}
}

// TestRedactProxy 验证账户列表中的代理密码被替换，用户名、地址与端口保留
func TestRedactProxy(t *testing.T) {
	for input, want := range map[string]string{
		"socks5://user:secret@10.0.0.2:1080": "socks5://user:xxxxx@10.0.0.2:1080",
		"http://127.0.0.1:7890":              "http://127.0.0.1:7890",
		"":                                   "",
	} {
		if got := redactProxy(input); got != want {
			t.Errorf("%q=%q 期望 %q", input, got, want)
		}
	}
}
