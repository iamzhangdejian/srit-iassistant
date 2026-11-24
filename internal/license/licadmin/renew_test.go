package licadmin

import (
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/license"
)

// TestRenewChainsAndRevokesOld 覆盖续期的主路径：
// 新码从「原到期日」顺延、带上 ParentID、旧码自动作废。
func TestRenewChainsAndRevokesOld(t *testing.T) {
	svc, _ := newTestService(t)
	first, err := svc.Issue(IssueRequest{
		MACs:     []string{"3c:22:fb:aa:bb:01"},
		Days:     30,
		User:     "甲",
		Org:      "某研究院",
		Features: []license.Feature{license.FeatureKB, license.FeatureChat},
	})
	if err != nil {
		t.Fatalf("issue: %v", err)
	}

	// 生效 10 天后续期：剩余 20 天不能被吞掉。
	base := time.Date(2026, 11, 20, 9, 0, 0, 0, time.UTC)
	svc.now = func() time.Time { return base.AddDate(0, 0, 10) }

	res, err := svc.Renew(first.Entry.ID, RenewRequest{Days: 365})
	if err != nil {
		t.Fatalf("renew: %v", err)
	}

	wantExp := time.Unix(first.Entry.ExpiresAt, 0).AddDate(0, 0, 365).Unix()
	if res.Entry.ExpiresAt != wantExp {
		t.Errorf("到期日应顺延到 %s，实际 %s",
			time.Unix(wantExp, 0).Format(time.RFC3339),
			time.Unix(res.Entry.ExpiresAt, 0).Format(time.RFC3339))
	}
	if res.Entry.ParentID != first.Entry.ID {
		t.Errorf("新码 ParentID = %d，期望 %d", res.Entry.ParentID, first.Entry.ID)
	}
	if res.Entry.User != "甲" || res.Entry.Org != "某研究院" {
		t.Errorf("续期应沿用使用人/单位，实际 %q/%q", res.Entry.User, res.Entry.Org)
	}
	if len(res.Entry.Features) != 2 {
		t.Errorf("续期应沿用功能位，实际 %v", res.Entry.Features)
	}

	old, ok := svc.store.Get(first.Entry.ID)
	if !ok {
		t.Fatal("旧记录不该消失")
	}
	if old.Status != StatusRevoked {
		t.Errorf("旧码状态 = %s，期望 %s", old.Status, StatusRevoked)
	}
	if old.RevokeReason != "已续期" {
		t.Errorf("旧码作废原因 = %q，期望 已续期", old.RevokeReason)
	}

	// 新码必须能通过公钥验签，否则客户拿到的是废码。
	if _, err := license.Decode(res.Entry.Code, svc.publicKey); err != nil {
		t.Fatalf("新码验签失败：%v", err)
	}
}

// TestRenewRejectsRevokedAndPermanent 覆盖两类不能续期的情况。
func TestRenewRejectsRevokedAndPermanent(t *testing.T) {
	svc, _ := newTestService(t)

	permanent, err := svc.Issue(IssueRequest{MACs: []string{"3c:22:fb:aa:bb:02"}, Days: 0})
	if err != nil {
		t.Fatalf("issue permanent: %v", err)
	}
	if _, err := svc.Renew(permanent.Entry.ID, RenewRequest{Days: 30}); err == nil {
		t.Error("永久授权不该允许续期")
	}

	revoked, err := svc.Issue(IssueRequest{MACs: []string{"3c:22:fb:aa:bb:03"}, Days: 30})
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	if _, err := svc.store.Revoke(revoked.Entry.ID, "测试作废", time.Now()); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if _, err := svc.Renew(revoked.Entry.ID, RenewRequest{Days: 30}); err == nil {
		t.Error("已作废的码不该允许续期")
	}

	if _, err := svc.Renew(99999, RenewRequest{Days: 30}); err == nil {
		t.Error("不存在的记录应当报错")
	}
}

// TestListPagePagination 覆盖分页与总数：页面要显示「共 N 条」。
func TestListPagePagination(t *testing.T) {
	svc, _ := newTestService(t)
	for i := 0; i < 5; i++ {
		mac := []string{"3c:22:fb:cc:01:0" + string(rune('0'+i))}
		if _, err := svc.Issue(IssueRequest{MACs: mac, Days: 30, User: "用户" + string(rune('A'+i))}); err != nil {
			t.Fatalf("issue %d: %v", i, err)
		}
	}

	page1, total := svc.store.ListPage(Query{Limit: 2})
	if total != 5 {
		t.Fatalf("total = %d，期望 5", total)
	}
	if len(page1) != 2 {
		t.Fatalf("第一页 %d 条，期望 2", len(page1))
	}
	page2, _ := svc.store.ListPage(Query{Limit: 2, Offset: 2})
	if len(page2) != 2 || page2[0].ID == page1[0].ID {
		t.Fatalf("第二页应当续接且不重复，实际 %+v vs %+v", page2, page1)
	}
	page3, _ := svc.store.ListPage(Query{Limit: 2, Offset: 4})
	if len(page3) != 1 {
		t.Fatalf("最后一页 %d 条，期望 1", len(page3))
	}
}
