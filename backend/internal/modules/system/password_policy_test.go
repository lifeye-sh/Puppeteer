package system

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestPasswordPolicyValidationAndUnicode(t *testing.T) {
	values := ConfigurationValues{"password-strength-type": "recommend", "password-strength-policy": DefaultPasswordPolicy}
	for _, password := range []string{"twelve", "password1234", "aaaaaaaaaaaa", "ALICE has a long phrase", "abc\nlong password"} {
		if ValidateNewPassword(values, "alice", password) == nil {
			t.Fatalf("accepted invalid password case %d", len(password))
		}
	}
	for _, password := range []string{"several separate memorable words", "这是一段足够长而且不包含账号的测试口令", "Mixed-phrase-123!"} {
		if err := ValidateNewPassword(values, "alice", password); err != nil {
			t.Fatal(err)
		}
	}
	p := PasswordPolicy{MinLength: 8, MinCharacterType: 3, MustNotContainUsername: true, MustNotBePalindrome: true, MustNotWeak: true}
	raw, _ := json.Marshal(p)
	values["password-strength-type"] = "customize"
	values["password-strength-policy"] = string(raw)
	for _, password := range []string{"Abcd1234", "ab1!!1ba", "alice12!"} {
		if ValidateNewPassword(values, "alice", password) == nil {
			t.Fatal("custom policy bypass")
		}
	}
	if err := ValidateNewPassword(values, "alice", "中文甲乙123!"); err != nil {
		t.Fatal(err)
	}
	if ValidateNewPassword(values, "alice", strings.Repeat("密", 400)+"1!") == nil {
		t.Fatal("byte limit bypass")
	}
	for _, invalid := range []string{`{}`, `null`, DefaultPasswordPolicy + `{}`, strings.Replace(DefaultPasswordPolicy, `"minLength":12`, `"minLength":12,"minLength":8`, 1), strings.Replace(DefaultPasswordPolicy, `"minLength":12`, `"minLength":7`, 1), strings.Replace(DefaultPasswordPolicy, `"minCharacterType":2`, `"minCharacterType":4`, 1), strings.Replace(DefaultPasswordPolicy, `"mustNotWeak":true`, `"mustNotWeak":null`, 1), strings.Replace(DefaultPasswordPolicy, `"minLength":12`, `"unknown":12`, 1)} {
		if _, err := ParsePasswordPolicy(invalid); err == nil {
			t.Fatal("invalid policy accepted")
		}
	}
}
