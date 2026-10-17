package eid

import (
	"testing"

	gxid "github.com/rs/xid"
)

// TestNewValidMatch 生成值必须通过自身校验、类型匹配且互不重复
func TestNewValidMatch(t *testing.T) {
	id := New(User)
	if !Valid(id) {
		t.Fatalf("New 生成的 %q 未通过 Valid", id)
	}
	if !Match(id, User) {
		t.Fatalf("New(User) 生成的 %q 未通过 Match(User)", id)
	}
	if Match(id, Session) {
		t.Fatalf("%q 不应匹配 Session", id)
	}
	if id2 := New(User); id2 == id {
		t.Fatalf("连续生成重复 ID %q", id)
	}

	// 全部注册类型均应生成可通过自身 Match 的值
	for ty := range registeredTypes {
		got := New(ty)
		if !Match(got, ty) {
			t.Errorf("New(%s) 生成的 %q 未通过 Match(%s)", ty, got, ty)
		}
	}
}

// TestValidInvalid 非法样例一律拒绝
func TestValidInvalid(t *testing.T) {
	bad := []string{
		"",
		"_",
		"usr_",
		"usr_not-an-xid",
		"xyz_" + gxid.New().String(),       // 未注册前缀
		gxid.New().String(),                // 无前缀裸 xid
		"usr_" + gxid.New().String() + "x", // XID 段超长
		"usr_" + gxid.New().String() + "_tail",
		"usr_" + gxid.New().String() + gxid.New().String(), // 超 VARCHAR(32) 上限
	}
	for _, s := range bad {
		if Valid(s) {
			t.Errorf("Valid(%q) 应为 false", s)
		}
	}
}

// TestNewUnknownTypePanics 未注册类型必须 panic，防生成无法解析的 ID
func TestNewUnknownTypePanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("New(未注册类型) 应 panic")
		}
	}()
	New(Type("xxx"))
}
