package internal

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/decodeex/bifu-payment-sdk/internal/strings2"
)

func GenerateSignature(accessKey, secretKey string, timestampMs int64) string {
	content := fmt.Sprintf("%s_%d", accessKey, timestampMs)

	secretKeyBytes := strings2.ToBytesNoAlloc(secretKey)
	hasher := hmac.New(sha256.New, secretKeyBytes)

	hasher.Write(strings2.ToBytesNoAlloc(content))
	mac := hasher.Sum(nil)

	return strings.ToUpper(hex.EncodeToString(mac))
}

func VerifySignature(accessKey, secretKey string, timestampMs int64, signature string) bool {
	expectedSignature := GenerateSignature(accessKey, secretKey, timestampMs)
	return strings.EqualFold(expectedSignature, signature)
}
