package system

import (
	"encoding/json"
	"errors"
	"io"
	"strings"
	"unicode"
	"unicode/utf8"
)

const DefaultPasswordPolicy = `{"minLength":12,"minCharacterType":2,"mustNotContainUsername":true,"mustNotBePalindrome":false,"mustNotWeak":true}`

type PasswordPolicy struct {
	MinLength              int  `json:"minLength"`
	MinCharacterType       int  `json:"minCharacterType"`
	MustNotContainUsername bool `json:"mustNotContainUsername"`
	MustNotBePalindrome    bool `json:"mustNotBePalindrome"`
	MustNotWeak            bool `json:"mustNotWeak"`
}

func ParsePasswordPolicy(raw string) (PasswordPolicy, error) {
	var policy PasswordPolicy
	decoder := json.NewDecoder(strings.NewReader(raw))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return policy, errors.New("invalid password policy")
	}
	seen := map[string]bool{}
	for decoder.More() {
		token, err = decoder.Token()
		if err != nil {
			return policy, err
		}
		key, ok := token.(string)
		if !ok || seen[key] {
			return policy, errors.New("duplicate password policy field")
		}
		seen[key] = true
		var value json.RawMessage
		if err = decoder.Decode(&value); err != nil || string(value) == "null" {
			return policy, errors.New("invalid password policy value")
		}
		switch key {
		case "minLength":
			err = json.Unmarshal(value, &policy.MinLength)
		case "minCharacterType":
			err = json.Unmarshal(value, &policy.MinCharacterType)
		case "mustNotContainUsername":
			err = json.Unmarshal(value, &policy.MustNotContainUsername)
		case "mustNotBePalindrome":
			err = json.Unmarshal(value, &policy.MustNotBePalindrome)
		case "mustNotWeak":
			err = json.Unmarshal(value, &policy.MustNotWeak)
		default:
			return policy, errors.New("unknown password policy field")
		}
		if err != nil {
			return policy, err
		}
	}
	if _, err = decoder.Token(); err != nil {
		return policy, err
	}
	if decoder.Decode(new(any)) != io.EOF || len(seen) != 5 || policy.MinLength < 8 || policy.MinLength > 128 || policy.MinCharacterType < 1 || policy.MinCharacterType > 3 {
		return policy, errors.New("invalid password policy")
	}
	return policy, nil
}

func validatePasswordConfiguration(values ConfigurationValues) error {
	if values.String("password-strength-type") != "recommend" && values.String("password-strength-type") != "customize" {
		return errors.New("invalid password policy type")
	}
	_, err := ParsePasswordPolicy(values.String("password-strength-policy"))
	return err
}

func ValidateNewPassword(values ConfigurationValues, username, password string) error {
	p, err := ParsePasswordPolicy(values.String("password-strength-policy"))
	if err != nil {
		return err
	}
	if values.String("password-strength-type") == "recommend" {
		p = PasswordPolicy{MinLength: 12, MinCharacterType: 1, MustNotContainUsername: true, MustNotWeak: true}
	} else if values.String("password-strength-type") != "customize" {
		return errors.New("密码策略无效，请联系管理员")
	}
	if !utf8.ValidString(password) || len(password) > 1024 || utf8.RuneCountInString(password) < p.MinLength {
		return errors.New("密码长度不符合当前策略（按字符计数，最多1024字节）")
	}
	lower := strings.ToLower(password)
	if p.MustNotContainUsername && username != "" && strings.Contains(lower, strings.ToLower(username)) {
		return errors.New("密码不能包含账号名称")
	}
	letters, numbers, other := false, false, false
	runes := []rune(password)
	palindrome, repeated := true, true
	for i, r := range runes {
		if unicode.IsControl(r) {
			return errors.New("密码不能包含控制字符")
		}
		if unicode.IsLetter(r) {
			letters = true
		} else if unicode.IsDigit(r) {
			numbers = true
		} else {
			other = true
		}
		palindrome = palindrome && r == runes[len(runes)-1-i]
		repeated = repeated && r == runes[0]
	}
	classes := 0
	for _, v := range []bool{letters, numbers, other} {
		if v {
			classes++
		}
	}
	if classes < p.MinCharacterType {
		return errors.New("密码的字母、数字、其他字符种类数不足")
	}
	if p.MustNotBePalindrome && palindrome {
		return errors.New("密码不能是回文")
	}
	if p.MustNotWeak {
		weak := map[string]bool{"password": true, "password123": true, "password1234": true, "password12345": true, "123456789012": true, "1234567890123456": true, "qwerty123456": true, "administrator": true, "changeme12345": true, "admin12345678": true, "welcome123456": true, "letmein123456": true}
		if repeated || weak[strings.TrimSpace(lower)] {
			return errors.New("请勿使用常见弱密码或重复字符密码")
		}
	}
	return nil
}
