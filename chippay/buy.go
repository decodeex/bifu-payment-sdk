package chippay

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/decode-ex/payment-sdk/internal/strings2"
	"github.com/shopspring/decimal"
)

type signer struct{}

func (signer) Sign(privateKey *rsa.PrivateKey, data map[string]string) (string, error) {
	hashed := sha256.Sum256(signer{}.encode(data))
	signature, err := rsa.SignPKCS1v15(rand.Reader, privateKey, crypto.SHA256, hashed[:])
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(signature), nil
}

func (signer) Verify(publicKey *rsa.PublicKey, data map[string]string, signature string) error {
	hashed := sha256.Sum256(signer{}.encode(data))
	signatureBytes, err := base64.StdEncoding.DecodeString(signature)
	if err != nil {
		return fmt.Errorf("failed to decode signature: %w", err)
	}
	return rsa.VerifyPKCS1v15(publicKey, crypto.SHA256, hashed[:], signatureBytes)
}

func (signer) encode(data map[string]string) []byte {
	keys := make([]string, 0, len(data))
	for k := range data {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var sb strings.Builder
	for i, key := range keys {
		sb.WriteString(key)
		sb.WriteString("=")
		sb.WriteString(data[key])
		if i == len(keys)-1 {
			break
		}
		sb.WriteString("&")
	}

	signString := sb.String()
	return strings2.ToBytesNoAlloc(signString)
}

// OrderType 订单类型
type OrderType = string

const (
	OrderTypeBuy  OrderType = "1" // 快捷买单
	OrderTypeSell OrderType = "2" // 快捷卖单
)

type IDCardType = int

const (
	IDCardTypeID       IDCardType = iota + 1 // 身份证
	IDCardTypePassport                       // 护照
	IDCardTypeOther                          // 其他
)

type OrderPayChannel = int

const (
	OrderPayChannel_MOMO     OrderPayChannel = iota + 1 // MOMO
	OrderPayChannel_Alipay   OrderPayChannel = 2        // 支付宝
	OrderPayChannel_BankCard OrderPayChannel = 3        // 银行卡
)

type CoinSign = string

const (
	CoinSignUSDT CoinSign = "USDT"
)

type PayCoinSign = string

const (
	PayCoinSignCNY = "cny"
	PayCoinSignVND = "vnd"
)

type rawBuyPayload struct {
	// 商户id
	CompanyID string `json:"companyId"`
	// 用户验证级别
	KYCLevel string `json:"-"` // `json:"kyc" default:"2"`
	// 真实姓名(接受简体中文和繁体中文与英文，中国客户一般为姓在前名在后，中间不留空格，建议传输中文字。username 根据payCoinSign 使用以下两种pattern:1.cny:([\s·\u4e00-\u9fa5]{2,15})\|([\s·A-Za-z]{2,35})2.vnd_false :.[`~!@#$%^&()+=|{}':;',[].<>?~！@#￥%……&（）——+|{}【】‘；：”“’。，、？\\d]+.如果字符串中包含这些特殊字符或数字，则会报错。 )
	// 中文2-15位;英文2-35位
	UserName string `json:"username"`
	// 国际区号
	AreaCode string `json:"areaCode,omitempty" default:"2"`
	// 手机号
	Phone string `json:"phone"`
	// 用户邮箱，只支持当payCoinSign为vnd时传输，phone或者email需择一传输
	Email string `json:"-"` // `json:"email,omitempty"`
	// 订单类型 1.快捷买单 2.快捷卖单
	OrderType OrderType `json:"orderType"`
	// 证件类型(1.身份证 2.护照 3.其他)
	IDCardType IDCardType `json:"-"` // `json:"idCardType,omitempty"`
	// 证件号码
	IDCardNum string `json:"-"` // `json:"idCardNum,omitempty"`
	// 银行卡号（快捷卖单必填）
	PayCardNo string `json:"-"` // `json:"payCardNo,omitempty"`
	// 开户银行（快捷卖单必填）,当payCoinSign为vnd时需准确填入银行名称, 参考[vnd区银行名称](https://open-v2.chippay.com/api/cnAPI.html#vnd_bank_area)
	PayCardBank string `json:"-"` // `json:"payCardBank,omitempty"`
	// 开户支行
	PayCardBranch string `json:"-"` // `json:"payCardBranch,omitempty"`
	// 商户订单号
	CompanyOrderNum string `json:"companyOrderNum"`
	// 数字货币标识(USDT)
	CoinSign CoinSign `json:"coinSign"`
	// 法币币别，须传小写英文(cny，vnd)
	PayCoinSign PayCoinSign `json:"payCoinSign"`
	// USDT下单数字货币数量 精度最多至小数点后4位(coinAmount和 total 两个字段二选一，
	// 当两个字段都填写的时候，优先处理total
	// coinAmount参数换算后的法币金额若不为整数，将无条件进位为整数显示于收银台
	CoinAmount string `json:"coinAmount"`
	// 用户付款的法币总金额(快捷买单只能传整数，快捷卖单不限)
	Total string `json:"total,omitempty"`
	// 当payCoinSign为cny时买单支持2.支付宝 , 3.银行卡方式，卖单支持 3.Bank card方式。payCoinSign为vnd时买单支持 1.MOMO , 3.Bank card 方式，卖单支持 3.Bank card 方式。
	OrderPayChannel OrderPayChannel `json:"orderPayChannel,omitempty" default:"3"`
	// 客户自定义单价（最多接收四位小数）详见[交易规则&常见问题](https://open-v2.chippay.com/api/cnAPI.html#trading_rules)
	DisplayUnitPrice string `json:"-"` // `json:"displayUnitPrice,omitempty"`
	// 订单时间戳（使用当前时间戳，与当前时间相差5分钟视为无效）, 单位毫秒
	OrderTime time.Time `json:"orderTime"`
	// 同步返回地址 (用户完成或取消交易后返回至商户平台的地址)
	SyncURL string `json:"syncUrl"`
	// 异步通知地址 (商户接收回调通知的地址)
	AsyncUrl string `json:"asyncUrl"`
	// 签名
	Signature string `json:"sign"`

	// 生成sign时使用, 同时用于序列化
	params map[string]string `json:"-"` //
}

func (raw *rawBuyPayload) MarshalJSON() ([]byte, error) {
	params := map[string]string{}
	for k, v := range raw.params {
		params[k] = v
	}
	params["sign"] = raw.Signature
	return json.Marshal(params)
}

func (raw *rawBuyPayload) serializeToMap() map[string]string {
	params := make(map[string]string)
	params["areaCode"] = raw.AreaCode
	params["asyncUrl"] = raw.AsyncUrl
	params["coinAmount"] = raw.CoinAmount
	params["coinSign"] = string(raw.CoinSign)
	params["companyId"] = raw.CompanyID
	params["companyOrderNum"] = raw.CompanyOrderNum
	params["orderPayChannel"] = strconv.Itoa(raw.OrderPayChannel)
	params["orderTime"] = strconv.FormatInt(raw.OrderTime.UnixMilli(), 10)
	params["orderType"] = raw.OrderType
	params["payCoinSign"] = string(raw.PayCoinSign)
	params["phone"] = raw.Phone
	params["syncUrl"] = raw.SyncURL
	params["total"] = raw.Total
	params["username"] = raw.UserName
	return params
}

func (raw *rawBuyPayload) generateSign(priavateKey *rsa.PrivateKey) (string, error) {
	raw.params = raw.serializeToMap()
	return signer{}.Sign(priavateKey, raw.params)
}

func (raw *rawBuyPayload) GenerateSignedRequest(ctx context.Context, conf *Config) (*http.Request, error) {
	const (
		Path        = "/cola/apiOpen/addOrder"
		Method      = http.MethodPost
		ContentType = "application/json"
	)
	sign, err := raw.generateSign(conf.privateKey)
	if err != nil {
		return nil, fmt.Errorf("failed to generate sign: %w", err)
	}
	raw.Signature = sign

	body, err := json.Marshal(raw)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request body: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, Method, Path, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", ContentType)

	return req, nil
}

func (raw *rawBuyPayload) Reply() rawBuyResponse {
	return rawBuyResponse{}
}

type rawBuyResponseData struct {
	Link    string `json:"link"`
	OrderNo string `json:"orderNo"`
}

type rawResponse[T any] struct {
	Code    StatusCode `json:"code"`
	Message string     `json:"msg"`
	Data    T          `json:"data"`
	Success bool       `json:"success"`
}

type rawBuyResponse = rawResponse[rawBuyResponseData]

type PaymentMethod = int

const (
	PaymentMethodBankCard PaymentMethod = 1 // 银行卡
	PaymentMethodAlipay   PaymentMethod = 2 // 支付宝
	PaymentMethodWeChat   PaymentMethod = 3 // 微信支付
)

type rawAddIntentOrderPayload struct {
	// 商户id
	CompanyID string `json:"companyId"`
	// 商户订单号
	CompanyOrderNum string `json:"companyOrderNum"`
	// 国际区号
	AreaCode string `json:"areaCode,omitempty" default:"86"`
	// 手机号
	Phone string `json:"phone,omitempty"`
	// 同步返回地址 (用户完成或取消交易后返回至商户平台的地址)
	SyncURL string `json:"syncUrl"`
	// 异步通知地址 (商户接收回调通知的地址)
	AsyncUrl string `json:"asyncUrl"`
	// 用户付款的法币总金额(只能传整数)
	// 币种cny
	TotalAmount int32 `json:"totalAmount,omitempty"`
	// USDT下单数字货币数量，精度最多至小数点后4位( TotalAmount 和 CoinQuantity 两个字段二选一，当两个字段都填写的时候，优先处理TotalAmount )
	// CoinQuantity参数换算后法币金额若不为整数将无条件进位为整数显示于收银台
	CoinQuantity decimal.Decimal `json:"coinQuantity,omitempty"`
	// 真实姓名(接受简体中文和繁体中文与英文，中国客户一般为姓在前名在后，中间不留空格，建议传输中文字。
	// 请使用以下pattern: ([\s·\u4e00-\u9fa5]{2,15})|([\s·A-Za-z]{2,35})
	Name string `json:"name,omitempty"`
	// 证件类型(1.身份证 2.护照 3.其他)
	IDCardType IDCardType `json:"idCardType,omitempty"`
	// 地区国家
	Area string `json:"area,omitempty"`
	// 证件号码
	Number string `json:"number,omitempty"`
	// 证件人像面URL
	IdentityPictureFront string `json:"identityPictureFront,omitempty"`
	// 证件无人像面URL
	IdentityPictureBack string `json:"identityPictureBack,omitempty"`
	// 附加证件 URL
	AdditionalPicture string `json:"additionalPicture,omitempty"`
	// 支付方式(用户指定只能使用的支付方式，若不指定则用户可自行选择支付方式。
	// 1.银行卡 2.支付宝支付 3.微信支付)
	PaymentMethod PaymentMethod `json:"paymentMethod,omitempty"`
	// 签名
	Sign string `json:"sign"`
}

func (raw *rawAddIntentOrderPayload) serializeToMap() map[string]string {
	params := map[string]string{
		"companyId":       raw.CompanyID,
		"companyOrderNum": raw.CompanyOrderNum,
		"syncUrl":         raw.SyncURL,
		"asyncUrl":        raw.AsyncUrl,
	}

	if raw.AreaCode != "" {
		params["areaCode"] = raw.AreaCode
	}

	if raw.Phone != "" {
		params["phone"] = raw.Phone
	}
	if raw.TotalAmount > 0 {
		params["totalAmount"] = strconv.Itoa(int(raw.TotalAmount))
	}
	if raw.CoinQuantity.GreaterThan(decimal.Zero) {
		params["coinQuantity"] = raw.CoinQuantity.StringFixed(4)
	}
	if raw.Name != "" {
		params["name"] = raw.Name
	}
	if raw.IDCardType > 0 {
		params["idCardType"] = strconv.Itoa(raw.IDCardType)
	}
	if raw.Area != "" {
		params["area"] = raw.Area
	}
	if raw.Number != "" {
		params["number"] = raw.Number
	}
	if raw.IdentityPictureFront != "" {
		params["identityPictureFront"] = raw.IdentityPictureFront
	}
	if raw.IdentityPictureBack != "" {
		params["identityPictureBack"] = raw.IdentityPictureBack
	}
	if raw.AdditionalPicture != "" {
		params["additionalPicture"] = raw.AdditionalPicture
	}
	if raw.PaymentMethod > 0 {
		params["paymentMethod"] = strconv.Itoa(raw.PaymentMethod)
	}
	return params
}

func (raw *rawAddIntentOrderPayload) generateSign(priavateKey *rsa.PrivateKey) (map[string]string, string, error) {
	params := raw.serializeToMap()
	sign, err := signer{}.Sign(priavateKey, params)
	if err != nil {
		return nil, "", fmt.Errorf("failed to generate sign: %w", err)
	}
	params["sign"] = sign
	return params, sign, nil
}

func (raw *rawAddIntentOrderPayload) GenerateSignedRequest(ctx context.Context, config *Config) (*http.Request, error) {
	const (
		Path        = "/cola/apiOpen/addIntentOrder"
		Method      = http.MethodPost
		ContentType = "application/json"
	)
	payload, _, err := raw.generateSign(config.privateKey)
	if err != nil {
		return nil, err
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request body: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, Method, Path, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", ContentType)

	return req, nil
}

func (raw *rawAddIntentOrderPayload) Reply() rawAddIntentOrderResponse {
	return rawAddIntentOrderResponse{}
}

type rawAddIntentOrderResponse = rawResponse[rawAddIntentOrderResponseData]

type rawAddIntentOrderResponseData struct {
	Link    string `json:"link"`
	OrderNo string `json:"orderNo"`
}

// https://open-v2.chippay.com/api/cnAPI.html#10106
type StatusCode = int

const (
	StatusCodeSuccess StatusCode = 200
)
