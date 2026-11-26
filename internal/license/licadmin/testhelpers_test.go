package licadmin

import (
	"crypto/ed25519"
	"encoding/base64"
	"os"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/license"
)

// writeTestKey 在 path 写一个确定性的测试私钥文件。
//
// 用固定种子而不是随机生成：测试失败时同一份种子能复现同一个密钥，
// 便于对照输出。生产密钥绝不会来自这里。
func writeTestKey(t *testing.T, path string) {
	t.Helper()
	seed := make([]byte, ed25519.SeedSize)
	for i := range seed {
		seed[i] = byte(30 + i)
	}
	priv := ed25519.NewKeyFromSeed(seed)
	text := base64.StdEncoding.EncodeToString(priv)
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatalf("write test key: %v", err)
	}
}

// LicenseCodeFromForeignKey 用「另一把私钥」签一张格式完全正确的码，
// 用来验证发码端不会误把别处签发的码当成自己的。
func LicenseCodeFromForeignKey(t *testing.T) string {
	t.Helper()
	seed := make([]byte, ed25519.SeedSize)
	for i := range seed {
		seed[i] = byte(200 - i)
	}
	priv := ed25519.NewKeyFromSeed(seed)
	now := time.Date(2026, 11, 20, 9, 0, 0, 0, time.UTC)
	payload := license.Payload{
		V:    1,
		SN:   "SN-FOREIGN-0001",
		FP:   license.Fingerprint([]string{"3c:22:fb:00:00:01"}),
		MAC:  []string{"3c:22:fb:00:00:01"},
		IAT:  now.Unix(),
		EXP:  now.AddDate(1, 0, 0).Unix(),
		Feat: []license.Feature{license.FeatureKB},
	}
	code, err := license.Encode(payload, priv)
	if err != nil {
		t.Fatalf("encode foreign key: %v", err)
	}
	return code
}
