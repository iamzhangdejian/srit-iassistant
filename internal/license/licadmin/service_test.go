package licadmin

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/license"
)

// 这批测试用固定的测试私钥，与 license 包的保持独立——
// 发码端与客户端共享的只有「格式」，不该共享「密钥」。
func newTestService(t *testing.T) (*Service, string) {
	t.Helper()
	dir := t.TempDir()
	privPath := filepath.Join(dir, "private.key")
	writeTestKey(t, privPath)
	svc, err := NewService(ServiceOptions{
		Store:          NewStore(filepath.Join(dir, "ledger.json")),
		PrivateKeyPath: privPath,
		Now:            func() time.Time { return time.Date(2026, 11, 20, 9, 0, 0, 0, time.UTC) },
	})
	if err != nil {
		t.Fatalf("new service: %v", err)
	}
	return svc, dir
}

// uniqueMAC 生成本次测试专用的 MAC，避免用例之间互相撞「已有授权码」。
func uniqueMAC(prefix int) string {
	return "3c:22:fb:00:00:" + string(rune('a'+prefix%26)) + string(rune('0'+prefix/26))
}

func TestIssueRejectsDuplicateMachine(t *testing.T) {
	svc, _ := newTestService(t)
	req := IssueRequest{MACs: []string{"3c:22:fb:dd:ee:01"}, Days: 30}

	if _, err := svc.Issue(req); err != nil {
		t.Fatalf("first issue: %v", err)
	}
	if _, err := svc.Issue(req); err == nil {
		t.Fatal("expected duplicate issue to be rejected")
	}
	// 强制签发应当允许。
	req.Force = true
	if _, err := svc.Issue(req); err != nil {
		t.Fatalf("forced issue: %v", err)
	}
}

func TestIssueBatchPartialFailure(t *testing.T) {
	svc, _ := newTestService(t)
	text := "MAC,使用人\n" +
		"3c:22:fb:00:00:01,甲\n" +
		"not-a-mac,乙\n" +
		"3c:22:fb:00:00:02,丙,单位,90,kb\n"

	res := svc.IssueBatch(text, IssueRequest{Days: 365})
	if res.Issued != 2 || res.Failed != 1 {
		t.Fatalf("issued=%d failed=%d, want 2/1 (rows=%+v)", res.Issued, res.Failed, res.Rows)
	}
	// 行内指定的 90 天必须覆盖默认的 365 天。
	for _, row := range res.Rows {
		if row.Entry != nil && row.Entry.User == "丙" {
			exp := time.Unix(row.Entry.ExpiresAt, 0)
			want := time.Date(2026, 11, 20, 9, 0, 0, 0, time.UTC).AddDate(0, 0, 90)
			if !exp.Equal(want) {
				t.Errorf("per-row days not honored: got %s, want %s", exp, want)
			}
		}
	}
}

func TestParseBatchSkipsHeaderAndComments(t *testing.T) {
	rows := ParseBatch("# 注释\n\nMAC,user\n3c:22:fb:00:00:01,甲\n")
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want 1 (%+v)", len(rows), rows)
	}
	if rows[0].Fields[0] != "3c:22:fb:00:00:01" {
		t.Errorf("unexpected first field %q", rows[0].Fields[0])
	}
}

// 批量页的提示与占位符都写「功能用 + 连接」，解析器必须认这个分隔符。
// 曾经漏了 '+'，用户照着示例填 kb+chat 会整行报「未知功能 "kb+chat"」。
func TestIssueBatchAcceptsPlusSeparatedFeatures(t *testing.T) {
	svc, _ := newTestService(t)
	res := svc.IssueBatch("3c:22:fb:00:00:02,甲,某院,30,kb+chat\n", IssueRequest{Days: 365})
	if res.Issued != 1 || res.Failed != 0 {
		t.Fatalf("issued=%d failed=%d, want 1/0 (%+v)", res.Issued, res.Failed, res.Rows)
	}
	got := res.Rows[0].Entry.Features
	want := []license.Feature{license.FeatureKB, license.FeatureChat}
	if len(got) != len(want) {
		t.Fatalf("features = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("features[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

// 一行里写了多个 MAC，只要有一个解析失败就必须整行报错：
// 用户以为绑了三块网卡、实际只绑上两块，换机时会被判机器不匹配。
func TestIssueBatchRejectsPartialMACs(t *testing.T) {
	svc, _ := newTestService(t)
	res := svc.IssueBatch("3c:22:fb:00:00:01;badmac,甲\n", IssueRequest{Days: 30})
	if res.Failed != 1 || res.Issued != 0 {
		t.Fatalf("issued=%d failed=%d, want 0/1", res.Issued, res.Failed)
	}
}

func TestStatusFilterAndSearch(t *testing.T) {
	svc, _ := newTestService(t)
	a, err := svc.Issue(IssueRequest{MACs: []string{"3c:22:fb:00:00:01"}, Days: 30, User: "甲"})
	if err != nil {
		t.Fatalf("issue a: %v", err)
	}
	if _, err := svc.Issue(IssueRequest{MACs: []string{"3c:22:fb:00:00:02"}, Days: 30, User: "乙", Org: "某单位"}); err != nil {
		t.Fatalf("issue b: %v", err)
	}

	if got := svc.store.List(Query{Keyword: "某单位"}); len(got) != 1 || got[0].User != "乙" {
		t.Errorf("keyword search = %+v, want the 乙 entry", got)
	}

	if _, err := svc.store.Revoke(a.Entry.ID, "测试作废", time.Now()); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if got := svc.store.List(Query{Status: StatusRevoked}); len(got) != 1 {
		t.Errorf("revoked filter = %d entries, want 1", len(got))
	}
	if got := svc.store.List(Query{Status: StatusActive}); len(got) != 1 {
		t.Errorf("active filter = %d entries, want 1", len(got))
	}
}

// 导出的授权码必须能被客户端解析——这是发码端与客户端之间唯一的契约，
// 一个字段拼错就会导致「码发下去了但用户激活不了」。
func TestIssuedCodeDecodesWithClientParser(t *testing.T) {
	svc, _ := newTestService(t)
	res, err := svc.Issue(IssueRequest{
		MACs:     []string{"3C-22-FB-DD-EE-01", "f0:18:98:dd:ee:02"},
		Days:     365,
		User:     "张三",
		Org:      "某研究院",
		Features: []license.Feature{license.FeatureKB, license.FeatureChat},
	})
	if err != nil {
		t.Fatalf("issue: %v", err)
	}

	parsed, err := license.Decode(res.Entry.Code, svc.publicKey)
	if err != nil {
		t.Fatalf("client parser rejected our code: %v", err)
	}
	if parsed.Payload.User != "张三" {
		t.Errorf("user = %q", parsed.Payload.User)
	}
	if len(parsed.Payload.Feat) != 2 {
		t.Errorf("features = %v, want 2", parsed.Payload.Feat)
	}
	// MAC 必须被归一化：大小写与分隔符统一才能与客户端比对成功。
	if !license.Matches(parsed.Payload.MAC, []string{"3c:22:fb:dd:ee:01"}) {
		t.Errorf("normalized MACs %v do not match the machine", parsed.Payload.MAC)
	}
	// 指纹也要与「用同样两块网卡算出来的指纹」一致。
	if parsed.Payload.FP != license.Fingerprint([]string{"3c:22:fb:dd:ee:01", "f0:18:98:dd:ee:02"}) {
		t.Errorf("fingerprint mismatch: %s", parsed.Payload.FP)
	}
}

func TestInspectReportsLedgerLink(t *testing.T) {
	svc, _ := newTestService(t)
	res, err := svc.Issue(IssueRequest{MACs: []string{"3c:22:fb:00:00:01"}, Days: 30})
	if err != nil {
		t.Fatalf("issue: %v", err)
	}

	got := svc.Inspect(res.Entry.Code)
	if !got.Valid {
		t.Fatalf("inspect valid code failed: %s", got.Error)
	}
	if got.LedgerID != res.Entry.ID {
		t.Errorf("ledger_id = %d, want %d", got.LedgerID, res.Entry.ID)
	}

	// 别的密钥签出来的码必须判无效。
	bad := LicenseCodeFromForeignKey(t)
	if got := svc.Inspect(bad); got.Valid {
		t.Error("expected foreign-signed code to be invalid")
	}
}

// 表头识别曾经踩过两个坑，都固化成用例：
//  1. 用「含 mac 字样」判表头，导致 `...;badmac` 这种真数据行被当表头丢掉；
//  2. 只在行号 0 判表头，导致表头上面有注释时表头被当数据行报错。
func TestLooksLikeHeaderVariants(t *testing.T) {
	headers := []string{"MAC", "mac", "MAC地址", "网卡地址", "机器指纹", "地址"}
	for _, h := range headers {
		if !looksLikeHeader([]string{h, "使用人"}) {
			t.Errorf("%q should be treated as a header row", h)
		}
	}

	// 真数据行（含分隔符与非法尾巴）绝不能被当表头。
	data := []string{
		"3c:22:fb:00:00:01",
		"3c:22:fb:00:00:01;badmac",
		"3C-22-FB-00-00-01",
		"3c22fb000001",
	}
	for _, d := range data {
		if looksLikeHeader([]string{d, "甲"}) {
			t.Errorf("%q is data, not a header", d)
		}
	}
}

// 只有一行真数据、没有表头时也必须签发成功。
func TestIssueBatchSingleDataRow(t *testing.T) {
	svc, _ := newTestService(t)
	res := svc.IssueBatch("3c:22:fb:00:00:09,甲\n", IssueRequest{Days: 30})
	if res.Issued != 1 || res.Failed != 0 {
		t.Fatalf("issued=%d failed=%d, want 1/0 (%+v)", res.Issued, res.Failed, res.Rows)
	}
}
