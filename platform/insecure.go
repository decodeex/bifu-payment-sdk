package platform

import (
	"crypto/tls"
	"net/http"
)

// insecureTransport 只为「自签证书的测试环境」存在。
//
// 单独放一个文件，是为了让它在代码审查里显眼：任何人搜 InsecureSkipVerify
// 都会先看到这段注释。生产环境把域名和正式证书配好之后，这个开关就不该再出现在配置里。
func insecureTransport() *http.Transport {
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} //nolint:gosec // 仅测试环境
	return t
}
