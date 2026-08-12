package shared

import (
	"net/http"
	"testing"
)

func TestGatewayClientErrorDetailPassesDefaultModelGuidance(t *testing.T) {
	detail := GatewayClientErrorDetail(
		http.StatusBadRequest,
		"默认模型仍使用 Provider kimi-code；请先在设置中切换默认模型",
	)
	if detail != "默认模型仍使用 Provider kimi-code；请先在设置中切换默认模型" {
		t.Fatalf("默认模型引导文案应透传给客户端: %q", detail)
	}
	if masked := GatewayClientErrorDetail(http.StatusBadRequest, "sql: syntax error"); masked != "请求参数错误" {
		t.Fatalf("内部错误细节仍应被收敛为通用文案: %q", masked)
	}
}
