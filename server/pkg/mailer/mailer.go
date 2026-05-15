// Package mailer wraps net/smtp behind a tiny interface. If SMTP isn't
// configured (no SMTP_HOST in env) we fall back to printing the mail to
// the logger — handy for dev and for early-stage prod where you want to
// pull the verification link out of `docker logs deploy-api-1`.
package mailer

import (
	"crypto/tls"
	"encoding/base64"
	"fmt"
	"log/slog"
	"net/smtp"
	"strings"
	"time"
)

type Config struct {
	Host     string // empty = dev/log mode
	Port     int
	User     string
	Password string
	From     string // From: header, e.g. "DFCHAT <no-reply@dfchat.chat>"
	UseTLS   bool   // implicit TLS (port 465 etc). Otherwise STARTTLS if available.
}

type Mailer struct {
	cfg Config
	log *slog.Logger
}

func New(cfg Config, log *slog.Logger) *Mailer {
	return &Mailer{cfg: cfg, log: log}
}

// Enabled reports whether real SMTP delivery is configured. Callers can
// still call Send when false; the mail just goes to the log.
func (m *Mailer) Enabled() bool { return m.cfg.Host != "" }

// Send delivers a plain-text email. Subject is the message subject;
// body is the plain-text body (UTF-8).
func (m *Mailer) Send(to, subject, body string) error {
	if !m.Enabled() {
		m.log.Info("mail (dev — no SMTP configured)",
			"to", to, "subject", subject, "body_preview", trunc(body, 240))
		return nil
	}

	addr := fmt.Sprintf("%s:%d", m.cfg.Host, m.cfg.Port)
	from := m.cfg.From
	if from == "" {
		from = m.cfg.User
	}

	msg := buildMessage(from, to, subject, body)

	if m.cfg.UseTLS {
		return sendImplicitTLS(addr, m.cfg.Host, m.cfg.User, m.cfg.Password, from, to, msg, m.log)
	}
	auth := smtp.PlainAuth("", m.cfg.User, m.cfg.Password, m.cfg.Host)
	if err := smtp.SendMail(addr, auth, from, []string{to}, msg); err != nil {
		m.log.Warn("smtp send failed", "to", to, "err", err.Error())
		return err
	}
	m.log.Info("mail sent", "to", to, "subject", subject)
	return nil
}

// Implicit-TLS path (port 465 style; the connection is wrapped in TLS
// before any SMTP commands). Aliyun DM / 腾讯 EDM / SendGrid all support
// either implicit-TLS-465 or STARTTLS-587 — choose with UseTLS flag.
func sendImplicitTLS(addr, host, user, pass, from, to string, body []byte, log *slog.Logger) error {
	conn, err := tls.Dial("tcp", addr, &tls.Config{ServerName: host})
	if err != nil {
		return err
	}
	defer conn.Close()
	c, err := smtp.NewClient(conn, host)
	if err != nil {
		return err
	}
	defer c.Quit()
	if user != "" {
		if err := c.Auth(smtp.PlainAuth("", user, pass, host)); err != nil {
			return err
		}
	}
	if err := c.Mail(from); err != nil {
		return err
	}
	if err := c.Rcpt(to); err != nil {
		return err
	}
	w, err := c.Data()
	if err != nil {
		return err
	}
	if _, err := w.Write(body); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	log.Info("mail sent (tls)", "to", to)
	return nil
}

func buildMessage(from, to, subject, body string) []byte {
	var b strings.Builder
	b.WriteString("From: ")
	b.WriteString(from)
	b.WriteString("\r\n")
	b.WriteString("To: ")
	b.WriteString(to)
	b.WriteString("\r\n")
	b.WriteString("Subject: ")
	b.WriteString(mimeBEncode(subject))
	b.WriteString("\r\n")
	b.WriteString("Date: ")
	b.WriteString(time.Now().UTC().Format(time.RFC1123Z))
	b.WriteString("\r\n")
	b.WriteString("MIME-Version: 1.0\r\n")
	b.WriteString("Content-Type: text/plain; charset=UTF-8\r\n")
	b.WriteString("Content-Transfer-Encoding: 8bit\r\n")
	b.WriteString("\r\n")
	b.WriteString(body)
	return []byte(b.String())
}

func mimeBEncode(s string) string {
	for _, r := range s {
		if r > 127 {
			return "=?UTF-8?B?" + base64.StdEncoding.EncodeToString([]byte(s)) + "?="
		}
	}
	return s
}

func trunc(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
