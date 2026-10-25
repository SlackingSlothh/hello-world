package dbops

import (
	"errors"
	"strings"
)

var ErrUnsafeQuery = errors.New("only a single read-only SELECT statement is allowed")

// ValidateSelect deliberately accepts only SELECT (not WITH) as the first SQL
// token and rejects statement separators outside quoted strings/comments.
// Dialect implementations also execute it in a read-only transaction.
func ValidateSelect(query string) error {
	tokens, separators, err := sqlTokens(query)
	if err != nil || separators > 0 || len(tokens) == 0 || tokens[0] != "SELECT" {
		return ErrUnsafeQuery
	}
	for _, token := range tokens {
		if token == "INTO" || token == "UPDATE" || token == "DELETE" || token == "INSERT" || token == "CALL" || token == "EXEC" {
			return ErrUnsafeQuery
		}
	}
	return nil
}

func sqlTokens(s string) ([]string, int, error) {
	var tokens []string
	var word strings.Builder
	flush := func() {
		if word.Len() > 0 {
			tokens = append(tokens, strings.ToUpper(word.String()))
			word.Reset()
		}
	}
	separators := 0
	quote := rune(0)
	for i := 0; i < len(s); i++ {
		c := rune(s[i])
		if quote != 0 {
			if c == quote {
				if i+1 < len(s) && rune(s[i+1]) == quote {
					i++
					continue
				}
				quote = 0
			}
			if c == '\\' && quote != '`' && i+1 < len(s) {
				i++
			}
			continue
		}
		if c == '\'' || c == '"' || c == '`' {
			flush()
			quote = c
			continue
		}
		if c == '-' && i+1 < len(s) && s[i+1] == '-' {
			flush()
			for i+1 < len(s) && s[i+1] != '\n' {
				i++
			}
			continue
		}
		if c == '/' && i+1 < len(s) && s[i+1] == '*' {
			flush()
			i += 2
			for i+1 < len(s) && !(s[i] == '*' && s[i+1] == '/') {
				i++
			}
			if i+1 >= len(s) {
				return nil, 0, ErrUnsafeQuery
			}
			i++
			continue
		}
		if c == ';' {
			flush()
			separators++
			continue
		}
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || c == '_' {
			word.WriteRune(c)
		} else {
			flush()
		}
	}
	flush()
	if quote != 0 {
		return nil, 0, ErrUnsafeQuery
	}
	return tokens, separators, nil
}
