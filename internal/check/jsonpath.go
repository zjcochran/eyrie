// SPDX-License-Identifier: AGPL-3.0-or-later

package check

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// A path picks one value out of a JSON document: object keys separated by
// dots, array indexes in brackets, and keys with other characters in
// brackets and double quotes. A leading "$" or "$." is optional.
//
//	status
//	$.data.items[0].state
//	checks["db.primary"].ok
type pathStep struct {
	key   string
	index int // -1 for a key
}

func parsePath(p string) ([]pathStep, error) {
	p = strings.TrimSpace(p)
	p = strings.TrimPrefix(strings.TrimPrefix(p, "$"), ".")
	if p == "" {
		return nil, errors.New("the JSON path is required, such as $.status")
	}
	bad := fmt.Errorf("%q is not a JSON path such as $.data.items[0].state", p)
	var steps []pathStep
	for p != "" {
		switch {
		case strings.HasPrefix(p, `["`):
			end := strings.Index(p, `"]`)
			if end < 0 {
				return nil, bad
			}
			steps = append(steps, pathStep{key: p[2:end], index: -1})
			p = p[end+2:]
		case p[0] == '[':
			end := strings.IndexByte(p, ']')
			n, err := strconv.Atoi(p[1:max(end, 1)])
			if end < 0 || err != nil || n < 0 {
				return nil, bad
			}
			steps = append(steps, pathStep{index: n})
			p = p[end+1:]
		default:
			end := strings.IndexAny(p, ".[")
			if end < 0 {
				end = len(p)
			}
			if end == 0 {
				return nil, bad
			}
			steps = append(steps, pathStep{key: p[:end], index: -1})
			p = p[end:]
		}
		if strings.HasPrefix(p, ".") {
			p = p[1:]
			if p == "" {
				return nil, bad
			}
		}
	}
	return steps, nil
}

// Query finds the value at path in a JSON document and returns it as text:
// strings as they are, other values as JSON.
func Query(doc []byte, path string) (string, error) {
	steps, err := parsePath(path)
	if err != nil {
		return "", err
	}
	var v any
	if err := json.Unmarshal(doc, &v); err != nil {
		return "", errors.New("the response is not JSON")
	}
	for _, s := range steps {
		switch cur := v.(type) {
		case map[string]any:
			next, ok := cur[s.key]
			if s.index >= 0 || !ok {
				return "", fmt.Errorf("%s: nothing at %s", path, s.name())
			}
			v = next
		case []any:
			if s.index < 0 || s.index >= len(cur) {
				return "", fmt.Errorf("%s: nothing at %s", path, s.name())
			}
			v = cur[s.index]
		default:
			return "", fmt.Errorf("%s: nothing at %s", path, s.name())
		}
	}
	if str, ok := v.(string); ok {
		return str, nil
	}
	b, _ := json.Marshal(v)
	return string(b), nil
}

func (s pathStep) name() string {
	if s.index >= 0 {
		return fmt.Sprintf("[%d]", s.index)
	}
	return strconv.Quote(s.key)
}

// operators compare a found value with the expected one.
var operators = []string{"==", "!=", "<", "<=", ">", ">=", "contains"}

// compare compares as numbers when both sides are numbers, else as text.
// contains looks for the expected text in the value.
func compare(got, op, want string) bool {
	if op == "contains" {
		return strings.Contains(got, want)
	}
	a, errA := strconv.ParseFloat(got, 64)
	b, errB := strconv.ParseFloat(want, 64)
	c := strings.Compare(got, want)
	if errA == nil && errB == nil {
		switch {
		case a < b:
			c = -1
		case a > b:
			c = 1
		default:
			c = 0
		}
	}
	switch op {
	case "==":
		return c == 0
	case "!=":
		return c != 0
	case "<":
		return c < 0
	case "<=":
		return c <= 0
	case ">":
		return c > 0
	}
	return c >= 0 // ">="
}
