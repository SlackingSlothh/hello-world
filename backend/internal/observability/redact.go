package observability

import (
	"fmt"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
)

const RedactedValue = "[REDACTED]"

var (
	uriCredentialPattern = regexp.MustCompile(`([a-zA-Z][a-zA-Z0-9+.-]*://[^/\s:@]+:)([^@\s/]+)(@)`)
	mysqlDSNPattern      = regexp.MustCompile(`(^|[\s"'=])([a-zA-Z0-9._-]+:)([^@\s"']+)(@tcp\(|@unix\()`)
	kvSecretPattern      = regexp.MustCompile(`(?i)\b(password|passwd|pwd|secret|token|session_id|sessionid|session|api_key|apikey|access_key|private_key|encryption_key)\b(\s*[:=]\s*)("[^"]*"|'[^']*'|[^\s,;&]+)`)
	bearerPattern        = regexp.MustCompile(`(?i)\b(Bearer|Basic)\s+([A-Za-z0-9._~+/=-]+)`)
	headerSecretPattern  = regexp.MustCompile(`(?i)\b(Cookie|Set-Cookie|Authorization|Proxy-Authorization)\s*:\s*([^\r\n]+)`)
	sqlSensitiveLiteral  = regexp.MustCompile(`(?i)\b([a-z0-9_]*(?:password|passwd|secret|token|session|credential|dsn)[a-z0-9_]*)\b(\s*(?:=|!=|<>|LIKE|ILIKE)\s*)('(?:''|[^'])*')`)
	sensitiveKeyFragments = []string{
		"password",
		"passwd",
		"secret",
		"token",
		"session_id",
		"sessionid",
		"cookie",
		"authorization",
		"dsn",
		"credential",
		"api_key",
		"apikey",
		"private_key",
		"encryption_key",
	}
)

func IsSensitiveKey(key string) bool {
	lower := strings.ToLower(strings.TrimSpace(key))
	if lower == "" {
		return false
	}
	for _, fragment := range sensitiveKeyFragments {
		if strings.Contains(lower, fragment) {
			return true
		}
	}
	return false
}

func RedactDSN(raw string) string {
	out := uriCredentialPattern.ReplaceAllString(raw, "${1}"+RedactedValue+"${3}")
	out = mysqlDSNPattern.ReplaceAllString(out, "${1}${2}"+RedactedValue+"${4}")
	out = kvSecretPattern.ReplaceAllString(out, "${1}${2}"+RedactedValue)
	return out
}

func RedactString(raw string) string {
	if raw == "" {
		return raw
	}
	out := RedactDSN(raw)
	out = headerSecretPattern.ReplaceAllString(out, "${1}: "+RedactedValue)
	out = bearerPattern.ReplaceAllString(out, "${1} "+RedactedValue)
	return out
}

func RedactHeaders(headers http.Header) map[string]string {
	sanitized := make(map[string]string, len(headers))
	for name, values := range headers {
		if IsSensitiveKey(name) {
			sanitized[name] = RedactedValue
			continue
		}
		joined := strings.Join(values, ", ")
		sanitized[name] = RedactString(joined)
	}
	return sanitized
}

func RedactSQL(query string) string {
	if query == "" {
		return query
	}
	out := sqlSensitiveLiteral.ReplaceAllString(query, "${1}${2}'"+RedactedValue+"'")
	out = RedactString(out)
	return out
}

func RedactParams(params []any) []any {
	if len(params) == 0 {
		return nil
	}
	out := make([]any, len(params))
	for i, param := range params {
		out[i] = redactValue(param)
	}
	return out
}

func RedactNamedParams(params map[string]any) map[string]any {
	if len(params) == 0 {
		return nil
	}
	out := make(map[string]any, len(params))
	for k, v := range params {
		if IsSensitiveKey(k) {
			out[k] = RedactedValue
			continue
		}
		out[k] = redactValue(v)
	}
	return out
}

func RedactAttr(attr slog.Attr) slog.Attr {
	if IsSensitiveKey(attr.Key) {
		return slog.String(attr.Key, RedactedValue)
	}
	val := attr.Value.Resolve()
	switch val.Kind() {
	case slog.KindString:
		s := val.String()
		if strings.EqualFold(attr.Key, "query") || strings.EqualFold(attr.Key, "sql") {
			return slog.String(attr.Key, RedactSQL(s))
		}
		return slog.String(attr.Key, RedactString(s))
	case slog.KindGroup:
		group := val.Group()
		sanitized := make([]any, 0, len(group))
		for _, item := range group {
			sanitized = append(sanitized, RedactAttr(item))
		}
		return slog.Group(attr.Key, sanitized...)
	case slog.KindAny:
		return slog.Any(attr.Key, redactValue(val.Any()))
	default:
		return attr
	}
}

func redactValue(v any) any {
	switch typed := v.(type) {
	case nil:
		return nil
	case string:
		return RedactString(typed)
	case []byte:
		return RedactString(string(typed))
	case error:
		return RedactString(typed.Error())
	case http.Header:
		return RedactHeaders(typed)
	case map[string]any:
		return RedactNamedParams(typed)
	case map[string]string:
		out := make(map[string]string, len(typed))
		for k, val := range typed {
			if IsSensitiveKey(k) {
				out[k] = RedactedValue
			} else {
				out[k] = RedactString(val)
			}
		}
		return out
	case []any:
		return RedactParams(typed)
	case []string:
		out := make([]string, len(typed))
		for i, item := range typed {
			out[i] = RedactString(item)
		}
		return out
	case fmt.Stringer:
		return RedactString(typed.String())
	default:
		return RedactString(fmt.Sprint(v))
	}
}
