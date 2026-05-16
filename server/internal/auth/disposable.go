package auth

import "strings"

// disposableEmailDomains is a curated list of the most common
// disposable / throwaway / temporary-mailbox providers. The point isn't
// to be exhaustive (community lists run to thousands of entries and
// rot fast) — it's to catch the lazy-95% case so spammers have to put
// in real effort. For higher-effort attackers we fall back to:
//   - per-IP rate limit (RateLimitStrict)
//   - mandatory email verification before send-message etc.
//   - background sweep that purges unverified accounts after 14 days
//
// All domains are lowercase; the lookup lowercases input.
var disposableEmailDomains = map[string]struct{}{
	// 10minutemail family
	"10minutemail.com":  {},
	"10minutemail.net":  {},
	"10minutemail.org":  {},
	"10minutemail.info": {},
	"10minutesmail.com": {},
	// Mailinator family
	"mailinator.com": {},
	"mailinator.net": {},
	"mailinator.org": {},
	"mailinator2.com": {},
	"binkmail.com":   {},
	"bobmail.info":   {},
	"chammy.info":    {},
	"devnullmail.com": {},
	"letthemeatspam.com": {},
	"mailinater.com": {},
	"mailinator.us":  {},
	"mailmetrash.com": {},
	"mailtothis.com": {},
	"notmailinator.com": {},
	"reallymymail.com": {},
	"reconmail.com":  {},
	"safetymail.info": {},
	"sendspamhere.com": {},
	"sogetthis.com":  {},
	"spambooger.com": {},
	"spamherelots.com": {},
	"spamhereplease.com": {},
	"spamthisplease.com": {},
	"streetwisemail.com": {},
	"suremail.info":  {},
	"thisisnotmyrealemail.com": {},
	"tradermail.info": {},
	"veryrealemail.com": {},
	"zoemail.net":    {},
	// Guerrillamail family
	"guerrillamail.com": {},
	"guerrillamail.biz": {},
	"guerrillamail.de":  {},
	"guerrillamail.info": {},
	"guerrillamail.net": {},
	"guerrillamail.org": {},
	"guerrillamailblock.com": {},
	"sharklasers.com": {},
	"grr.la":         {},
	// YOPmail
	"yopmail.com":  {},
	"yopmail.fr":   {},
	"yopmail.net":  {},
	"cool.fr.nf":   {},
	"jetable.fr.nf": {},
	"nospam.ze.tc": {},
	"nomail.xl.cx": {},
	"mega.zik.dj":  {},
	"speed.1s.fr":  {},
	// Throwaway / temp common
	"temp-mail.org":    {},
	"temp-mail.io":     {},
	"tempmail.com":     {},
	"tempmail.net":     {},
	"tempmailaddress.com": {},
	"tempmailo.com":    {},
	"tempinbox.com":    {},
	"throwawaymail.com": {},
	"throwam.com":      {},
	"trashmail.com":    {},
	"trashmail.de":     {},
	"trashmail.net":    {},
	"trashmail.io":     {},
	"trashmail.ws":     {},
	"trash-mail.com":   {},
	"trashinbox.com":   {},
	"dispostable.com":  {},
	"discard.email":    {},
	"discardmail.com":  {},
	"dropmail.me":      {},
	"emailondeck.com":  {},
	"fakeinbox.com":    {},
	"fake-mail.net":    {},
	"fakemail.fr":      {},
	"getairmail.com":   {},
	"getnada.com":      {},
	"harakirimail.com": {},
	"inboxbear.com":    {},
	"maildrop.cc":      {},
	"mail-temp.com":    {},
	"mailcatch.com":    {},
	"mailnesia.com":    {},
	"moakt.com":        {},
	"mohmal.com":       {},
	"mytemp.email":     {},
	"nada.email":       {},
	"spamgourmet.com":  {},
	"trbvm.com":        {},
	"yepmail.net":      {},
	"emltmp.com":       {},
	"linshiyouxiang.net": {},
	// Common CN throwaway
	"24hinbox.com":  {},
	"buyme.com":     {},
	"smashmail.de":  {},
	// fastest-mail aliases
	"fakemailgenerator.com": {},
	"33mail.com":            {},

	// Internal sentinel — SoftDelete rewrites scrubbed users' email to
	// `deleted_<id>@deleted.invalid`. Block external registration on the
	// same domain so deletion flows can't conflict.
	"deleted.invalid": {},
	// RFC 2606 reserved TLDs — should never reach a real MX. Blocking
	// them keeps test/junk addresses out of the user table.
	"example.com":  {},
	"example.org":  {},
	"example.net":  {},
	"localhost":    {},
}

// isDisposableEmail reports whether the email's domain is in the
// disposable list. Returns false for malformed input — caller is
// expected to have run the email regex first.
func isDisposableEmail(email string) bool {
	at := strings.LastIndexByte(email, '@')
	if at < 0 || at == len(email)-1 {
		return false
	}
	domain := strings.ToLower(email[at+1:])
	_, blocked := disposableEmailDomains[domain]
	return blocked
}
