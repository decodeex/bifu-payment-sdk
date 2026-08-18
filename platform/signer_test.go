package platform

import (
	"encoding/json"
	"net/url"
	"os"
	"testing"
)

// 跨语言签名向量。
//
// testdata/signature-vectors.json 与中台
// (payment-platform: packages/shared/src/crypto/signature-vectors.json)
// 是**同一份文件**：两个仓库读同一批向量、断言同一批期望签名。
// 这样签名对不上的时候，红的那一边就是错的那一边，不用靠猜 —— 而跨语言的
// 签名不一致是最难排查的一类问题。
func TestSignatureVectors(t *testing.T) {
	raw, err := os.ReadFile("testdata/signature-vectors.json")
	if err != nil {
		t.Fatalf("读取向量文件: %v", err)
	}
	var doc struct {
		Vectors []struct {
			Name      string `json:"name"`
			Why       string `json:"why"`
			Secret    string `json:"secret"`
			Method    string `json:"method"`
			Path      string `json:"path"`
			Timestamp int64  `json:"timestamp"`
			Body      string `json:"body"`
			Expected  string `json:"expected"`
		} `json:"vectors"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("解析向量文件: %v", err)
	}
	// 空数组会让下面的循环静默通过
	if len(doc.Vectors) == 0 {
		t.Fatal("向量文件里没有向量")
	}

	for _, v := range doc.Vectors {
		got := signRequest(v.Secret, signatureParts{
			method:    v.Method,
			path:      v.Path,
			timestamp: v.Timestamp,
			body:      []byte(v.Body),
		})
		if got != v.Expected {
			t.Errorf("%s\n  理由: %s\n  期望 %s\n  实际 %s", v.Name, v.Why, v.Expected, got)
		}
	}
}

// requestURI 必须带上 query。用 URL.Path 的话 ?channelNo=... 不在签名里，
// 一个签名就能打任意参数 —— 中台侧会验签失败，但那时已经很难看出是这里的问题。
func TestRequestURIIncludesQuery(t *testing.T) {
	u, err := url.Parse("https://hub.example.com/api/internal/config/fx?channelNo=000001&direction=DEPOSIT")
	if err != nil {
		t.Fatal(err)
	}
	got := requestURI(u)
	want := "/api/internal/config/fx?channelNo=000001&direction=DEPOSIT"
	if got != want {
		t.Fatalf("期望 %q，实际 %q", got, want)
	}
	if u.Path == got {
		t.Fatal("URL.Path 与 RequestURI() 相同，说明这个用例没有真的覆盖 query")
	}
}

func TestCanonicalStringSeparators(t *testing.T) {
	// 不加分隔符直接拼接的话，method 与 path 的边界可以左右挪动，
	// 两个不同的请求会算出同一个签名
	a := signRequest("k", signatureParts{method: "GET", path: "/a/b", timestamp: 1, body: nil})
	b := signRequest("k", signatureParts{method: "GET/a", path: "/b", timestamp: 1, body: nil})
	if a == b {
		t.Fatal("method 与 path 的边界被挪动后签名相同")
	}
}

func TestSignatureCoversEveryPart(t *testing.T) {
	base := signatureParts{method: "POST", path: "/api/gateway/quote", timestamp: 1_770_000_000_000, body: []byte(`{"amount":"100"}`)}
	ref := signRequest("k", base)

	cases := map[string]signatureParts{
		"改 method":    {method: "DELETE", path: base.path, timestamp: base.timestamp, body: base.body},
		"改 path":      {method: base.method, path: "/api/gateway/route", timestamp: base.timestamp, body: base.body},
		"改 timestamp": {method: base.method, path: base.path, timestamp: base.timestamp + 1, body: base.body},
		"改 body":      {method: base.method, path: base.path, timestamp: base.timestamp, body: []byte(`{"amount":"1000"}`)},
	}
	for name, c := range cases {
		if signRequest("k", c) == ref {
			t.Errorf("%s 之后签名没变 —— 说明这一项没进签名", name)
		}
	}
	if signRequest("other", base) == ref {
		t.Error("换密钥之后签名没变")
	}
}
