package internal

import "testing"

func TestGenerateSignature(t *testing.T) {
	const (
		accessKey   = "B6QKwx0NnKaQ14zf24Ux5Oc9Gy1xlf2R"
		secretKey   = "WUYx7DTQZakugtP9gOAimYUphcnc3jWuPRi1UVnWmwXSnMnsCVBzz1ILdaxisvz9"
		timestampMs = 1625546438154

		expectedSignature = "EDB15CF33C232128BDF118CEB147C453181939F8B37EC43886F68B3BCC2C19CD"
	)

	signature := GenerateSignature(accessKey, secretKey, timestampMs)
	if signature != expectedSignature {
		t.Errorf("expected %s, got %s", expectedSignature, signature)
	}
}
