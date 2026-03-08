// Package notify sends security alert emails using a configurable SMTP relay.
// It supports both authenticated and unauthenticated (relay-only) SMTP servers.
package notify

import (
	"bytes"
	"crypto/tls"
	"fmt"
	"log"
	"net"
	"net/smtp"
	"strings"
	"text/template"
	"time"

	"github.com/ghostersk/gowebmail/config"
)

// BruteForceAlert holds the data for the brute-force notification email.
type BruteForceAlert struct {
	Username    string
	ToEmail     string
	AttackerIP  string
	Country     string
	CountryCode string
	Attempts    int
	BlockedAt   time.Time
	BanHours    int // 0 = permanent
	AppName     string
	Hostname    string
}

var bruteForceTemplate = template.Must(template.New("brute").Parse(`From: {{.AppName}} Security <{{.From}}>
To: {{.ToEmail}}
Subject: Security Alert: Failed login attempts on your account
MIME-Version: 1.0
Content-Type: text/plain; charset=utf-8

Hello {{.Username}},

This is an automated security alert from {{.AppName}} ({{.Hostname}}).

We detected multiple failed login attempts on your account and have
automatically blocked the source IP address.

  Account targeted : {{.Username}}
  Source IP        : {{.AttackerIP}}
{{- if .Country}}
  Country          : {{.Country}} ({{.CountryCode}})
{{- end}}
  Failed attempts  : {{.Attempts}}
  Detected at      : {{.BlockedAt.Format "2006-01-02 15:04:05 UTC"}}
{{- if eq .BanHours 0}}
  Block duration   : Permanent (administrator action required to unblock)
{{- else}}
  Block duration   : {{.BanHours}} hours
{{- end}}

If this was you, you may have mistyped your password. The block will
{{- if eq .BanHours 0}} remain until removed by an administrator.
{{- else}} expire automatically after {{.BanHours}} hours.{{end}}

If you did not attempt to log in, your account credentials may be at
risk. We recommend changing your password as soon as possible.

This is an automated message. Please do not reply.

-- 
{{.AppName}} Security
{{.Hostname}}
`))

type templateData struct {
	BruteForceAlert
	From string
}

// SendBruteForceAlert sends a security notification email to the targeted user.
// It runs in a goroutine — errors are logged but not returned.
func SendBruteForceAlert(cfg *config.Config, alert BruteForceAlert) {
	if !cfg.NotifyEnabled || cfg.NotifySMTPHost == "" || cfg.NotifyFrom == "" {
		return
	}
	if alert.ToEmail == "" {
		return
	}
	go func() {
		if err := sendAlert(cfg, alert); err != nil {
			log.Printf("notify: failed to send brute-force alert to %s: %v", alert.ToEmail, err)
		} else {
			log.Printf("notify: sent brute-force alert to %s (attacker: %s)", alert.ToEmail, alert.AttackerIP)
		}
	}()
}

func sendAlert(cfg *config.Config, alert BruteForceAlert) error {
	if alert.AppName == "" {
		alert.AppName = "GoWebMail"
	}
	if alert.Hostname == "" {
		alert.Hostname = cfg.Hostname
	}

	data := templateData{BruteForceAlert: alert, From: cfg.NotifyFrom}
	var buf bytes.Buffer
	if err := bruteForceTemplate.Execute(&buf, data); err != nil {
		return fmt.Errorf("template execute: %w", err)
	}

	addr := fmt.Sprintf("%s:%d", cfg.NotifySMTPHost, cfg.NotifySMTPPort)

	// Choose auth method
	var auth smtp.Auth
	if cfg.NotifyUser != "" && cfg.NotifyPass != "" {
		auth = smtp.PlainAuth("", cfg.NotifyUser, cfg.NotifyPass, cfg.NotifySMTPHost)
	}

	// Try STARTTLS first (port 587), fall back to plain, support TLS on 465
	if cfg.NotifySMTPPort == 465 {
		return sendTLS(addr, cfg.NotifySMTPHost, auth, cfg.NotifyFrom, alert.ToEmail, buf.Bytes())
	}
	return sendSTARTTLS(addr, cfg.NotifySMTPHost, auth, cfg.NotifyFrom, alert.ToEmail, buf.Bytes())
}

// sendSTARTTLS sends via plain SMTP with optional STARTTLS upgrade (ports 25, 587).
func sendSTARTTLS(addr, host string, auth smtp.Auth, from, to string, msg []byte) error {
	c, err := smtp.Dial(addr)
	if err != nil {
		return fmt.Errorf("dial %s: %w", addr, err)
	}
	defer c.Close()

	// Try STARTTLS — not all servers require it (plain relay servers often skip it)
	if ok, _ := c.Extension("STARTTLS"); ok {
		tlsCfg := &tls.Config{ServerName: host}
		if err := c.StartTLS(tlsCfg); err != nil {
			// Log but continue — some relays advertise STARTTLS but don't enforce it
			log.Printf("notify: STARTTLS failed for %s, continuing unencrypted: %v", host, err)
		}
	}

	if auth != nil {
		if err := c.Auth(auth); err != nil {
			return fmt.Errorf("smtp auth: %w", err)
		}
	}

	return sendMessage(c, from, to, msg)
}

// sendTLS sends via direct TLS connection (port 465).
func sendTLS(addr, host string, auth smtp.Auth, from, to string, msg []byte) error {
	tlsCfg := &tls.Config{ServerName: host}
	conn, err := tls.Dial("tcp", addr, tlsCfg)
	if err != nil {
		return fmt.Errorf("tls dial %s: %w", addr, err)
	}

	// Resolve host for the smtp.NewClient call
	bareHost, _, _ := net.SplitHostPort(addr)
	if bareHost == "" {
		bareHost = host
	}

	c, err := smtp.NewClient(conn, bareHost)
	if err != nil {
		return fmt.Errorf("smtp client: %w", err)
	}
	defer c.Close()

	if auth != nil {
		if err := c.Auth(auth); err != nil {
			return fmt.Errorf("smtp auth: %w", err)
		}
	}

	return sendMessage(c, from, to, msg)
}

func sendMessage(c *smtp.Client, from, to string, msg []byte) error {
	if err := c.Mail(from); err != nil {
		return fmt.Errorf("MAIL FROM: %w", err)
	}
	if err := c.Rcpt(to); err != nil {
		return fmt.Errorf("RCPT TO: %w", err)
	}
	w, err := c.Data()
	if err != nil {
		return fmt.Errorf("DATA: %w", err)
	}
	// Normalise line endings to CRLF
	normalized := strings.ReplaceAll(string(msg), "\r\n", "\n")
	normalized = strings.ReplaceAll(normalized, "\n", "\r\n")
	if _, err := w.Write([]byte(normalized)); err != nil {
		return fmt.Errorf("write body: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("close data: %w", err)
	}
	return c.Quit()
}
