package internal

import (
	"crypto/md5"
	"encoding/hex"
	"sort"
	"strings"

	"github.com/decodeex/bifu-payment-sdk/internal/strings2"
)

type signEntry struct {
	Key   string
	Value string
}
type signer struct{}

func (signer) Sign(privateKey string, entries ...signEntry) string {
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Key < entries[j].Key
	})

	var signString strings.Builder
	for _, entry := range entries {
		signString.WriteString(entry.Key)
		signString.WriteString("=")
		signString.WriteString(entry.Value)
		signString.WriteString("&")
	}
	signString.WriteString("key=")
	signString.WriteString(privateKey)

	content := signString.String()
	bs := md5.Sum(strings2.ToBytesNoAlloc(content))
	return hex.EncodeToString(bs[:])
}
