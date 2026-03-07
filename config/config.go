// Package config loads and persists GoMail configuration from data/gomail.conf
package config

import (
	"bufio"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
)

// Config holds all application configuration.
type Config struct {
	// Server
	ListenAddr string // e.g. ":8080" or "0.0.0.0:8080"
	ListenPort string // derived from ListenAddr, e.g. "8080"
	Hostname   string // e.g. "mail.example.com" — used for BASE_URL and host checks
	BaseURL    string // auto-built from Hostname + ListenPort, or overridden explicitly

	// Security
	EncryptionKey  []byte // 32 bytes / AES-256
	SessionSecret  []byte
	SecureCookie   bool
	SessionMaxAge  int
	TrustedProxies []net.IPNet // CIDR ranges allowed to set X-Forwarded-For/Proto headers

	// Storage
	DBPath string

	// Google OAuth2
	GoogleClientID     string
	GoogleClientSecret string
	GoogleRedirectURL  string // auto-derived from BaseURL if blank

	// Microsoft OAuth2
	MicrosoftClientID     string
	MicrosoftClientSecret string
	MicrosoftTenantID     string
	MicrosoftRedirectURL  string // auto-derived from BaseURL if blank
}

const configPath = "./data/gomail.conf"

type configField struct {
	key      string
	defVal   string
	comments []string
}

// allFields is the single source of truth for config keys.
// Adding a field here causes it to automatically appear in gomail.conf on next startup.
var allFields = []configField{
	{
		key:    "HOSTNAME",
		defVal: "localhost",
		comments: []string{
			"--- Server ---",
			"Public hostname of this GoMail instance (no port, no protocol).",
			"Examples: localhost | mail.example.com | 192.168.1.10",
			"Used to build BASE_URL and OAuth redirect URIs automatically.",
			"Also used in security checks to reject requests with unexpected Host headers.",
		},
	},
	{
		key:    "LISTEN_ADDR",
		defVal: ":8080",
		comments: []string{
			"Address and port to listen on. Format: [host]:port",
			"  :8080          — all interfaces, port 8080",
			"  0.0.0.0:8080   — all interfaces (explicit)",
			"  127.0.0.1:8080 — localhost only",
		},
	},
	{
		key:    "BASE_URL",
		defVal: "",
		comments: []string{
			"Public URL of this instance (no trailing slash). Leave blank to auto-build",
			"from HOSTNAME and LISTEN_ADDR port (recommended).",
			"  Auto-build examples:",
			"    HOSTNAME=localhost       + :8080  → http://localhost:8080",
			"    HOSTNAME=mail.example.com + :443  → https://mail.example.com",
			"    HOSTNAME=mail.example.com + :8080 → http://mail.example.com:8080",
			"Override here only if you need a custom path prefix or your proxy rewrites the URL.",
		},
	},
	{
		key:    "SECURE_COOKIE",
		defVal: "false",
		comments: []string{
			"Set to true when GoMail is served over HTTPS (directly or via proxy).",
			"Marks session cookies as Secure so browsers only send them over TLS.",
		},
	},
	{
		key:    "SESSION_MAX_AGE",
		defVal: "604800",
		comments: []string{
			"How long a login session lasts, in seconds. Default: 604800 (7 days).",
		},
	},
	{
		key:    "TRUSTED_PROXIES",
		defVal: "",
		comments: []string{
			"Comma-separated list of IP addresses or CIDR ranges of trusted reverse proxies.",
			"Requests from these IPs may set X-Forwarded-For and X-Forwarded-Proto headers,",
			"which GoMail uses to determine the real client IP and whether TLS is in use.",
			"  Examples:",
			"    127.0.0.1                        (loopback only — Nginx/Traefik on same host)",
			"    10.0.0.0/8,172.16.0.0/12         (private networks)",
			"    192.168.1.50,192.168.1.51         (specific IPs)",
			"  Leave blank to disable proxy trust (requests are taken at face value).",
			"  NOTE: Do not add untrusted IPs — clients could spoof their source address.",
		},
	},
	{
		key:    "DB_PATH",
		defVal: "./data/gomail.db",
		comments: []string{
			"--- Storage ---",
			"Path to the SQLite database file.",
		},
	},
	{
		key:    "ENCRYPTION_KEY",
		defVal: "",
		comments: []string{
			"AES-256 key protecting all sensitive data at rest (emails, tokens, MFA secrets).",
			"Must be exactly 64 hex characters (= 32 bytes). Auto-generated on first run.",
			"NOTE: Back this up. Losing it makes the entire database permanently unreadable.",
		},
	},
	{
		key:    "SESSION_SECRET",
		defVal: "",
		comments: []string{
			"Secret used to sign session cookies. Auto-generated on first run.",
			"Changing this invalidates all active sessions (everyone gets logged out).",
		},
	},
	{
		key:    "GOOGLE_CLIENT_ID",
		defVal: "",
		comments: []string{
			"--- Gmail / Google OAuth2 ---",
			"Create at: https://console.cloud.google.com/apis/credentials",
			"  Application type : Web application",
			"  Required scope   : https://mail.google.com/",
			"  Redirect URI     : <BASE_URL>/auth/gmail/callback",
		},
	},
	{
		key:      "GOOGLE_CLIENT_SECRET",
		defVal:   "",
		comments: []string{},
	},
	{
		key:    "GOOGLE_REDIRECT_URL",
		defVal: "",
		comments: []string{
			"Override the Gmail OAuth redirect URL. Leave blank to auto-derive from BASE_URL.",
			"Must exactly match what is registered in Google Cloud Console.",
		},
	},
	{
		key:    "MICROSOFT_CLIENT_ID",
		defVal: "",
		comments: []string{
			"--- Outlook / Microsoft 365 OAuth2 ---",
			"Register at: https://portal.azure.com/#blade/Microsoft_AAD_RegisteredApps",
			"  Required API permissions : IMAP.AccessAsUser.All, SMTP.Send, offline_access, openid, email",
			"  Redirect URI             : <BASE_URL>/auth/outlook/callback",
		},
	},
	{
		key:      "MICROSOFT_CLIENT_SECRET",
		defVal:   "",
		comments: []string{},
	},
	{
		key:    "MICROSOFT_TENANT_ID",
		defVal: "common",
		comments: []string{
			"Use 'common' to allow any Microsoft account,",
			"or your Azure tenant ID to restrict to one organisation.",
		},
	},
	{
		key:    "MICROSOFT_REDIRECT_URL",
		defVal: "",
		comments: []string{
			"Override the Outlook OAuth redirect URL. Leave blank to auto-derive from BASE_URL.",
			"Must exactly match what is registered in Azure.",
		},
	},
}

// Load reads/creates data/gomail.conf, fills in missing keys, then returns Config.
// Environment variables override file values when set.
func Load() (*Config, error) {
	if err := os.MkdirAll("./data", 0700); err != nil {
		return nil, fmt.Errorf("create data dir: %w", err)
	}

	existing, err := readConfigFile(configPath)
	if err != nil {
		return nil, err
	}

	// Auto-generate secrets if missing
	if existing["ENCRYPTION_KEY"] == "" {
		existing["ENCRYPTION_KEY"] = mustHex(32)
		fmt.Println("WARNING: Generated new ENCRYPTION_KEY — it is saved in data/gomail.conf — back it up!")
	}
	if existing["SESSION_SECRET"] == "" {
		existing["SESSION_SECRET"] = mustHex(32)
	}

	// Write back (preserves existing, adds any new fields from allFields)
	if err := writeConfigFile(configPath, existing); err != nil {
		return nil, fmt.Errorf("write config: %w", err)
	}

	// get returns env var if set, else file value, else ""
	get := func(key string) string {
		// Only check env vars that are explicitly GoMail-namespaced or well-known.
		// We deliberately do NOT fall back to generic vars like PORT to avoid
		// picking up cloud-platform env vars unintentionally.
		if v := os.Getenv("GOMAIL_" + key); v != "" {
			return v
		}
		if v := os.Getenv(key); v != "" {
			return v
		}
		return existing[key]
	}

	// ---- Resolve listen address ----
	listenAddr := get("LISTEN_ADDR")
	if listenAddr == "" {
		listenAddr = ":8080"
	}
	// Ensure it has a port
	if !strings.Contains(listenAddr, ":") {
		listenAddr = ":" + listenAddr
	}
	_, listenPort, err := net.SplitHostPort(listenAddr)
	if err != nil {
		return nil, fmt.Errorf("invalid LISTEN_ADDR %q: %w", listenAddr, err)
	}

	// ---- Resolve hostname ----
	hostname := get("HOSTNAME")
	if hostname == "" {
		hostname = "localhost"
	}
	// Strip any accidental protocol or port from hostname
	hostname = strings.TrimPrefix(hostname, "http://")
	hostname = strings.TrimPrefix(hostname, "https://")
	hostname = strings.Split(hostname, ":")[0]
	hostname = strings.TrimRight(hostname, "/")

	// ---- Build BASE_URL ----
	baseURL := get("BASE_URL")
	if baseURL == "" {
		baseURL = buildBaseURL(hostname, listenPort)
	}
	// Strip trailing slash
	baseURL = strings.TrimRight(baseURL, "/")

	// ---- OAuth redirect URLs (auto-derive if blank) ----
	googleRedirect := get("GOOGLE_REDIRECT_URL")
	if googleRedirect == "" {
		googleRedirect = baseURL + "/auth/gmail/callback"
	}
	outlookRedirect := get("MICROSOFT_REDIRECT_URL")
	if outlookRedirect == "" {
		outlookRedirect = baseURL + "/auth/outlook/callback"
	}

	// ---- Decode secrets ----
	encHex := get("ENCRYPTION_KEY")
	encKey, err := hex.DecodeString(encHex)
	if err != nil || len(encKey) != 32 {
		return nil, fmt.Errorf("ENCRYPTION_KEY must be 64 hex chars (32 bytes), got %d chars", len(encHex))
	}

	sessSecret := get("SESSION_SECRET")
	if sessSecret == "" {
		return nil, fmt.Errorf("SESSION_SECRET is empty — this should not happen")
	}

	// ---- Trusted proxies ----
	trustedProxies, err := parseCIDRList(get("TRUSTED_PROXIES"))
	if err != nil {
		return nil, fmt.Errorf("invalid TRUSTED_PROXIES: %w", err)
	}

	cfg := &Config{
		ListenAddr:     listenAddr,
		ListenPort:     listenPort,
		Hostname:       hostname,
		BaseURL:        baseURL,
		DBPath:         get("DB_PATH"),
		EncryptionKey:  encKey,
		SessionSecret:  []byte(sessSecret),
		SecureCookie:   atobool(get("SECURE_COOKIE"), false),
		SessionMaxAge:  atoi(get("SESSION_MAX_AGE"), 604800),
		TrustedProxies: trustedProxies,

		GoogleClientID:        get("GOOGLE_CLIENT_ID"),
		GoogleClientSecret:    get("GOOGLE_CLIENT_SECRET"),
		GoogleRedirectURL:     googleRedirect,
		MicrosoftClientID:     get("MICROSOFT_CLIENT_ID"),
		MicrosoftClientSecret: get("MICROSOFT_CLIENT_SECRET"),
		MicrosoftTenantID:     orDefault(get("MICROSOFT_TENANT_ID"), "common"),
		MicrosoftRedirectURL:  outlookRedirect,
	}

	// Derive SECURE_COOKIE automatically if BASE_URL uses https
	if strings.HasPrefix(baseURL, "https://") && !cfg.SecureCookie {
		cfg.SecureCookie = true
	}

	logStartupInfo(cfg)
	return cfg, nil
}

// buildBaseURL constructs the public URL from hostname and port.
// Port 443 → https://<hostname>, port 80 → http://<hostname>,
// anything else → http://<hostname>:<port>
func buildBaseURL(hostname, port string) string {
	switch port {
	case "443":
		return "https://" + hostname
	case "80":
		return "http://" + hostname
	default:
		return "http://" + hostname + ":" + port
	}
}

// IsAllowedHost returns true if the request Host header matches our expected hostname.
// Accepts exact match, hostname:port, or any value if hostname is "localhost" (dev mode).
func (c *Config) IsAllowedHost(requestHost string) bool {
	if c.Hostname == "localhost" {
		return true // dev mode — permissive
	}
	// Strip port from request Host header
	h := requestHost
	if host, _, err := net.SplitHostPort(requestHost); err == nil {
		h = host
	}
	return strings.EqualFold(h, c.Hostname)
}

// RealIP extracts the genuine client IP from the request, honouring X-Forwarded-For
// only when the request comes from a trusted proxy.
func (c *Config) RealIP(remoteAddr string, xForwardedFor string) string {
	// Parse remote addr
	remoteIP, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		remoteIP = remoteAddr
	}

	if xForwardedFor == "" || !c.isTrustedProxy(remoteIP) {
		return remoteIP
	}

	// Take the left-most (client) IP from X-Forwarded-For
	parts := strings.Split(xForwardedFor, ",")
	if len(parts) > 0 {
		ip := strings.TrimSpace(parts[0])
		if net.ParseIP(ip) != nil {
			return ip
		}
	}
	return remoteIP
}

// IsHTTPS returns true if the request arrived over TLS, either directly
// or as indicated by X-Forwarded-Proto from a trusted proxy.
func (c *Config) IsHTTPS(remoteAddr string, xForwardedProto string) bool {
	if xForwardedProto != "" {
		remoteIP, _, err := net.SplitHostPort(remoteAddr)
		if err != nil {
			remoteIP = remoteAddr
		}
		if c.isTrustedProxy(remoteIP) {
			return strings.EqualFold(xForwardedProto, "https")
		}
	}
	return strings.HasPrefix(c.BaseURL, "https://")
}

func (c *Config) isTrustedProxy(ipStr string) bool {
	ip := net.ParseIP(ipStr)
	if ip == nil {
		return false
	}
	for _, cidr := range c.TrustedProxies {
		if cidr.Contains(ip) {
			return true
		}
	}
	return false
}

// ---- Config file I/O ----

func readConfigFile(path string) (map[string]string, error) {
	values := make(map[string]string)
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return values, nil
	}
	if err != nil {
		return nil, fmt.Errorf("open config: %w", err)
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		idx := strings.IndexByte(line, '=')
		if idx < 0 {
			continue
		}
		key := strings.TrimSpace(line[:idx])
		val := strings.TrimSpace(line[idx+1:])
		values[key] = val
	}
	return values, scanner.Err()
}

func writeConfigFile(path string, values map[string]string) error {
	var sb strings.Builder
	sb.WriteString("# GoMail Configuration\n")
	sb.WriteString("# =====================\n")
	sb.WriteString("# Auto-generated and updated on each startup.\n")
	sb.WriteString("# Edit freely — your values are always preserved.\n")
	sb.WriteString("# Environment variables (or GOMAIL_<KEY>) override values here.\n")
	sb.WriteString("#\n\n")

	for _, field := range allFields {
		for _, c := range field.comments {
			if c == "" {
				sb.WriteString("#\n")
			} else {
				sb.WriteString("# " + c + "\n")
			}
		}
		val := values[field.key]
		if val == "" {
			val = field.defVal
		}
		sb.WriteString(field.key + " = " + val + "\n\n")
	}
	return os.WriteFile(path, []byte(sb.String()), 0600)
}

// ---- Admin settings API ----

// EditableKeys lists config keys that may be changed via the admin UI.
// SESSION_SECRET and ENCRYPTION_KEY are intentionally excluded.
var EditableKeys = func() map[string]bool {
	excluded := map[string]bool{
		"SESSION_SECRET": true,
		"ENCRYPTION_KEY": true,
	}
	m := map[string]bool{}
	for _, f := range allFields {
		if !excluded[f.key] {
			m[f.key] = true
		}
	}
	return m
}()

// GetSettings returns the current raw config file values for all editable keys.
func GetSettings() (map[string]string, error) {
	raw, err := readConfigFile(configPath)
	if err != nil {
		return nil, err
	}
	result := make(map[string]string, len(allFields))
	for _, f := range allFields {
		if EditableKeys[f.key] {
			if v, ok := raw[f.key]; ok {
				result[f.key] = v
			} else {
				result[f.key] = f.defVal
			}
		}
	}
	return result, nil
}

// SetSettings merges the provided map into the config file.
// Only EditableKeys are accepted; unknown or protected keys are silently ignored.
// Returns the list of keys that were actually changed.
func SetSettings(updates map[string]string) ([]string, error) {
	raw, err := readConfigFile(configPath)
	if err != nil {
		return nil, err
	}
	var changed []string
	for k, v := range updates {
		if !EditableKeys[k] {
			continue
		}
		if raw[k] != v {
			raw[k] = v
			changed = append(changed, k)
		}
	}
	if len(changed) == 0 {
		return nil, nil
	}
	return changed, writeConfigFile(configPath, raw)
}

// ---- Host validation middleware helper ----

// HostCheck returns an HTTP middleware that rejects requests with unexpected Host headers.
// Skipped in dev mode (hostname == "localhost").
func (c *Config) HostCheckMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !c.IsAllowedHost(r.Host) {
			http.Error(w, "Invalid host header", http.StatusBadRequest)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// ---- Helpers ----

func parseCIDRList(s string) ([]net.IPNet, error) {
	var nets []net.IPNet
	if s == "" {
		return nets, nil
	}
	for _, raw := range strings.Split(s, ",") {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		// Allow bare IPs (treat as /32 or /128)
		if !strings.Contains(raw, "/") {
			ip := net.ParseIP(raw)
			if ip == nil {
				return nil, fmt.Errorf("invalid IP %q", raw)
			}
			bits := 32
			if ip.To4() == nil {
				bits = 128
			}
			raw = fmt.Sprintf("%s/%d", ip.String(), bits)
		}
		_, ipNet, err := net.ParseCIDR(raw)
		if err != nil {
			return nil, fmt.Errorf("invalid CIDR %q: %w", raw, err)
		}
		nets = append(nets, *ipNet)
	}
	return nets, nil
}

func logStartupInfo(cfg *Config) {
	fmt.Printf("GoMail starting:\n")
	fmt.Printf("  Listen  : %s\n", cfg.ListenAddr)
	fmt.Printf("  Base URL: %s\n", cfg.BaseURL)
	fmt.Printf("  Hostname: %s\n", cfg.Hostname)
	if len(cfg.TrustedProxies) > 0 {
		cidrs := make([]string, len(cfg.TrustedProxies))
		for i, n := range cfg.TrustedProxies {
			cidrs[i] = n.String()
		}
		fmt.Printf("  Proxies : %s\n", strings.Join(cidrs, ", "))
	}
}

func mustHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic("crypto/rand unavailable: " + err.Error())
	}
	return hex.EncodeToString(b)
}

func orDefault(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

func atoi(s string, fallback int) int {
	if v, err := strconv.Atoi(s); err == nil {
		return v
	}
	return fallback
}

func atobool(s string, fallback bool) bool {
	if v, err := strconv.ParseBool(s); err == nil {
		return v
	}
	return fallback
}

// Needed for HostCheckMiddleware
