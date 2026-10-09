package main

import "testing"

// TestClassify 验证按当前数据库版本划分迁移文件可修改性的逻辑
//
// 规则：版本 <= 当前版本（已应用）禁止修改；版本 > 当前版本（尚未应用）可修改；
// current=0 表示库未创建或未迁移，全部可修改。
func TestClassify(t *testing.T) {
	files := []string{"0001_users.up.sql", "0002_tokens.up.sql", "0003_follows.up.sql"}

	tests := []struct {
		name         string
		current      int64
		wantEditable []bool
	}{
		{"库未创建时全部可修改", 0, []bool{true, true, true}},
		{"已应用到0002时仅0003可修改", 2, []bool{false, false, true}},
		{"已应用到0001时后两个可修改", 1, []bool{false, true, true}},
		{"全部已应用时无可修改", 3, []bool{false, false, false}},
		{"版本超出文件范围时全部锁定", 9, []bool{false, false, false}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := classify(files, tt.current)
			if len(got) != len(tt.wantEditable) {
				t.Fatalf("classify() 返回 %d 项，期望 %d 项", len(got), len(tt.wantEditable))
			}
			for i, st := range got {
				if st.Editable != tt.wantEditable[i] {
					t.Errorf("%s 可修改性 = %v，期望 %v", st.Name, st.Editable, tt.wantEditable[i])
				}
			}
		})
	}
}
