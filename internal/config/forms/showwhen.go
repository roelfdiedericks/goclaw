package forms

import (
	"fmt"
	"reflect"
	"strings"
)

type showWhenOperator string

const (
	showWhenEq  showWhenOperator = "="
	showWhenNeq showWhenOperator = "!="
)

type ShowWhenClause struct {
	FieldPath string
	Operator  showWhenOperator
	Value     string
}

// ParseShowWhen parses a ShowWhen string into OR clauses.
// Supported syntax:
//   - "field=value"
//   - "field!=value"
//   - multiple clauses separated by "," (logical OR)
func ParseShowWhen(expr string) ([]ShowWhenClause, error) {
	expr = strings.TrimSpace(expr)
	if expr == "" {
		return nil, nil
	}

	rawClauses := strings.Split(expr, ",")
	clauses := make([]ShowWhenClause, 0, len(rawClauses))
	for _, raw := range rawClauses {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}

		var clause ShowWhenClause
		if idx := strings.Index(raw, "!="); idx > 0 {
			clause.FieldPath = strings.TrimSpace(raw[:idx])
			clause.Operator = showWhenNeq
			clause.Value = strings.TrimSpace(raw[idx+2:])
		} else if idx := strings.Index(raw, "="); idx > 0 {
			clause.FieldPath = strings.TrimSpace(raw[:idx])
			clause.Operator = showWhenEq
			clause.Value = strings.TrimSpace(raw[idx+1:])
		} else {
			return nil, fmt.Errorf("invalid showWhen clause %q", raw)
		}

		if clause.FieldPath == "" {
			return nil, fmt.Errorf("empty field path in showWhen clause %q", raw)
		}
		clauses = append(clauses, clause)
	}
	return clauses, nil
}

// EvaluateShowWhen evaluates a ShowWhen expression against a struct value.
// Invalid expressions preserve legacy behavior by returning true (section visible).
func EvaluateShowWhen(expr string, rv reflect.Value) bool {
	clauses, err := ParseShowWhen(expr)
	if err != nil || len(clauses) == 0 {
		return false
	}

	for _, c := range clauses {
		if evaluateClause(c, rv) {
			return true
		}
	}
	return false
}

func evaluateClause(c ShowWhenClause, rv reflect.Value) bool {
	fv, err := ResolveJSONPathStrict(rv, c.FieldPath)
	if err != nil || !fv.IsValid() {
		return false
	}
	actualValue := fmt.Sprintf("%v", fv.Interface())

	switch c.Operator {
	case showWhenEq:
		return actualValue == c.Value
	case showWhenNeq:
		return actualValue != c.Value
	default:
		return true
	}
}

// CompileShowWhenToAlpine compiles ShowWhen into an Alpine x-show expression.
// Invalid expressions return empty string to avoid breaking rendering.
func CompileShowWhenToAlpine(expr, dataVar string) string {
	clauses, err := ParseShowWhen(expr)
	if err != nil || len(clauses) == 0 {
		return ""
	}

	parts := make([]string, 0, len(clauses))
	for _, c := range clauses {
		jsPath := dataVar + "." + strings.TrimSpace(c.FieldPath)
		var part string
		switch c.Value {
		case "true":
			if c.Operator == showWhenNeq {
				part = "!" + jsPath
			} else {
				part = jsPath
			}
		case "false":
			if c.Operator == showWhenNeq {
				part = jsPath
			} else {
				part = "!" + jsPath
			}
		default:
			value := strings.ReplaceAll(c.Value, "'", "\\'")
			if c.Operator == showWhenNeq {
				part = fmt.Sprintf("%s !== '%s'", jsPath, value)
			} else {
				part = fmt.Sprintf("%s === '%s'", jsPath, value)
			}
		}
		parts = append(parts, part)
	}
	if len(parts) == 1 {
		return parts[0]
	}
	return "(" + strings.Join(parts, " || ") + ")"
}
