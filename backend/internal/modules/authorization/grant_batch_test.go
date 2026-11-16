package authorization

import (
	"fmt"
	"testing"
	"time"
)

func TestNormalizeGrantBatch(t *testing.T) {
	id := "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	resource := "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
	v, err := NormalizeGrantBatch(GrantBatch{UserIDs: []string{id, "AAAAAAAA-AAAA-4AAA-8AAA-AAAAAAAAAAAA"}, DepartmentIDs: []string{id}, ResourceIDs: []string{resource, resource}})
	if err != nil || len(v.UserIDs) != 1 || len(v.DepartmentIDs) != 1 || len(v.ResourceIDs) != 1 || v.ValidFrom.IsZero() {
		t.Fatal(v, err)
	}
	invalid := []GrantBatch{{}, {UserIDs: []string{id}}, {ResourceIDs: []string{resource}}, {UserIDs: []string{"invalid"}, ResourceIDs: []string{resource}}, {UserIDs: []string{id}, ResourceIDs: []string{resource}, StrategyID: "invalid"}}
	past := time.Now().Add(-time.Hour)
	invalid = append(invalid, GrantBatch{UserIDs: []string{id}, ResourceIDs: []string{resource}, ExpiresAt: &past})
	for _, in := range invalid {
		if _, err := NormalizeGrantBatch(in); err == nil {
			t.Fatal("invalid batch accepted", in)
		}
	}
	var subjects, resources []string
	for i := 0; i < 100; i++ {
		subjects = append(subjects, fmt.Sprintf("00000000-0000-4000-8000-%012d", i))
	}
	for i := 0; i < 6; i++ {
		resources = append(resources, fmt.Sprintf("11111111-1111-4111-8111-%012d", i))
	}
	if _, err := NormalizeGrantBatch(GrantBatch{UserIDs: subjects, ResourceIDs: resources[:5]}); err != nil {
		t.Fatal("500 relation boundary rejected", err)
	}
	if _, err := NormalizeGrantBatch(GrantBatch{UserIDs: subjects, ResourceIDs: resources}); err == nil {
		t.Fatal("oversized product accepted")
	}
}
