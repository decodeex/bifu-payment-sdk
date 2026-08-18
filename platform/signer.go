package platform

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/url"
	"strconv"
	"strings"
)

// 请求签名。必须与中台的实现逐字节一致
// （payment-platform: packages/shared/src/crypto/request-signature.ts）。
//
// 规范化串：
//
//	METHOD \n path(含 query) \n timestamp(Unix 毫秒) \n hex(sha256(body))
//
// 三个最容易写错的地方，各有测试钉住：
//
//  1. path **要带 query**。中台签的是 originalUrl，这里必须用 URL.RequestURI()
//     而不是 URL.Path —— 少了 query，一个签名就能打任意参数
//  2. body 签的是**原文字节**。序列化一次、发送另一次的话，map 的键顺序可能不同，
//     签名就时不时对不上，而这种失败极难复现
//  3. timestamp 是 **Unix 毫秒**，不是秒
const signatureAlgVersion = "hmac-sha256"

type signatureParts struct {
	method    string
	path      string // 含 query
	timestamp int64  // Unix 毫秒
	body      []byte
}

func canonicalString(p signatureParts) string {
	sum := sha256.Sum256(p.body)
	return strings.Join([]string{
		strings.ToUpper(p.method),
		p.path,
		strconv.FormatInt(p.timestamp, 10),
		hex.EncodeToString(sum[:]),
	}, "\n")
}

func signRequest(secret string, p signatureParts) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(canonicalString(p)))
	return hex.EncodeToString(mac.Sum(nil))
}

// requestURI 从 URL 里取出参与签名的那一段（路径 + query）。
//
// 单独抽出来是因为 url.URL 上有 Path / RawPath / RequestURI() 三个看起来都对的东西，
// 只有 RequestURI() 会带上 query。
func requestURI(u *url.URL) string {
	return u.RequestURI()
}
