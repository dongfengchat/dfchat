package auth

import (
	"strings"
	"unicode"
)

// commonPasswords is a small list of the most-leaked / most-guessed
// passwords. Hits ~95% of casual reuse attacks (cf. HIBP password lists
// where this set dominates by orders of magnitude). We don't pull in
// zxcvbn — that's a 100kB+ dataset for marginal gain over a quick deny
// list + structural checks below.
//
// All entries are lowercase; the comparison lowercases input.
var commonPasswords = map[string]struct{}{
	"password":     {},
	"password1":    {},
	"password123":  {},
	"password!":    {},
	"p@ssword":     {},
	"p@ssw0rd":     {},
	"qwerty":       {},
	"qwerty123":    {},
	"qwertyuiop":   {},
	"asdfasdf":     {},
	"asdfghjkl":    {},
	"zxcvbnm":      {},
	"12345678":     {},
	"123456789":    {},
	"1234567890":   {},
	"123123123":    {},
	"abc12345":     {},
	"abcd1234":     {},
	"abcdefgh":     {},
	"11111111":     {},
	"00000000":     {},
	"iloveyou":     {},
	"princess":     {},
	"admin123":     {},
	"administrator": {},
	"letmein":      {},
	"letmein1":     {},
	"welcome":      {},
	"welcome1":     {},
	"welcome123":   {},
	"monkey":       {},
	"monkey123":    {},
	"dragon":       {},
	"sunshine":     {},
	"baseball":     {},
	"football":     {},
	"superman":     {},
	"batman":       {},
	"trustno1":     {},
	"master":       {},
	"shadow":       {},
	"michael":      {},
	"jennifer":     {},
	"jordan23":     {},
	"hunter2":      {},
	"freedom":      {},
	"whatever":     {},
	"changeme":     {},
	"changeit":     {},
	"default":      {},
	"secret":       {},
	"login":        {},
	"passw0rd":     {},
	"passw0rd1":    {},
	"dfchat":       {},
	"dfchat123":    {},
	"dongfeng":     {},
	"dongfengchat": {},
	"woaini":       {},
	"woaini1314":   {},
	"5201314":      {},
	"woaini520":    {},
	"woaini521":    {},
	"a123456789":   {},
	"q123456789":   {},
	"q1w2e3r4":     {},
	"q1w2e3r4t5":   {},
	"asd123":       {},
	"qaz123":       {},
	"qazwsx":       {},
	"qazwsxedc":    {},
	"123qwe":       {},
	"qwer1234":     {},
}

// validatePassword returns an error message (in Chinese) describing why
// the password is too weak, or "" if it passes. Caller has already
// confirmed length is 8..72 bytes.
//
// Rules — fast structural checks tuned for password-spray resistance:
//   1. Reject members of commonPasswords (case-insensitive).
//   2. Reject all-same-character runs ("aaaaaaaa", "11111111").
//   3. Require at least two distinct character classes (digit /
//      letter / symbol). Mixed-case still counts as one class (letters).
//      "abcdefgh" or "12345678" alone → rejected.
func validatePassword(pw string) string {
	lower := strings.ToLower(pw)
	if _, common := commonPasswords[lower]; common {
		return "密码过于常见，请换一个更复杂的"
	}
	if isAllSameRune(pw) {
		return "密码不能全是同一个字符"
	}

	var hasDigit, hasLetter, hasSymbol bool
	for _, r := range pw {
		switch {
		case unicode.IsDigit(r):
			hasDigit = true
		case unicode.IsLetter(r):
			hasLetter = true
		default:
			hasSymbol = true
		}
	}
	classes := 0
	if hasDigit {
		classes++
	}
	if hasLetter {
		classes++
	}
	if hasSymbol {
		classes++
	}
	if classes < 2 {
		return "密码需要混合至少两种：字母、数字、符号"
	}
	return ""
}

func isAllSameRune(s string) bool {
	if s == "" {
		return false
	}
	first := rune(0)
	for i, r := range s {
		if i == 0 {
			first = r
			continue
		}
		if r != first {
			return false
		}
	}
	return true
}
