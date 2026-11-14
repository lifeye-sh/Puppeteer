package application

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"next-terminal/backend/internal/platform/mysqlproxy"
)

func TestDatabaseDiagnosticProfiles(t *testing.T) {
	for _, tc := range []struct {
		name, profile string
		err           error
		want          string
	}{
		{"readonly", "readonly", mysqlproxy.ErrTargetPrivileges, "只读凭据权限超出范围"},
		{"workorder", "workorder", mysqlproxy.ErrTargetPrivileges, "审批执行凭据权限超出范围"},
		{"dml common validation", "native-dml", mysqlproxy.ErrTargetPrivileges, "原生 DML 凭据仅允许"},
		{"dml strict validation", "native-dml", mysqlproxy.ErrDMLPrivileges, "原生 DML 凭据仅允许"},
		{"protocol failure", "readonly", errors.New("private connection details"), "目标连接或协议校验失败"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := databaseDiagnosticMessage(fmt.Errorf("wrapped: %w", tc.err), tc.profile)
			if !strings.HasPrefix(got, tc.want) || strings.Contains(got, "private connection details") {
				t.Fatalf("unexpected diagnostic: %s", got)
			}
		})
	}
}
