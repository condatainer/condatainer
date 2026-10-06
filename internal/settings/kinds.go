package settings

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/condatainer/condatainer/internal/utils"
)

// kind owns how text is parsed and normalized for one type of key.
type kind struct {
	name    string
	list    bool
	parse   func(k *Key, text string) (string, error)
	accepts func(k *Key) string
	suggest func(k *Key) []string
	show    func(stored string) string // optional; the stored text is shown when nil
}

func noSuggest(*Key) []string { return nil }

var boolKind = kind{
	name: "bool",
	parse: func(_ *Key, text string) (string, error) {
		b, err := strconv.ParseBool(text)
		if err != nil {
			return "", fmt.Errorf("%q is not a boolean: use true or false", text)
		}
		return strconv.FormatBool(b), nil
	},
	accepts: func(*Key) string { return "true or false" },
	suggest: func(*Key) []string { return []string{"true", "false"} },
}

func parseInt(k *Key, text string) (string, error) {
	n, err := strconv.Atoi(text)
	if err != nil {
		return "", fmt.Errorf("%q is not a whole number", text)
	}
	if k.opts.min != nil && n < *k.opts.min {
		return "", fmt.Errorf("%d is below the minimum %d", n, *k.opts.min)
	}
	if k.opts.max != nil && n > *k.opts.max {
		return "", fmt.Errorf("%d is above the maximum %d", n, *k.opts.max)
	}
	return strconv.Itoa(n), nil
}

func intAccepts(unit string) func(*Key) string {
	return func(k *Key) string {
		s := "a whole number" + unit
		switch {
		case k.opts.min != nil && k.opts.max != nil:
			s += fmt.Sprintf(" from %d to %d", *k.opts.min, *k.opts.max)
		case k.opts.min != nil:
			s += fmt.Sprintf(" of at least %d", *k.opts.min)
		case k.opts.max != nil:
			s += fmt.Sprintf(" of at most %d", *k.opts.max)
		}
		return s
	}
}

var intKind = kind{name: "int", parse: parseInt, accepts: intAccepts(""), suggest: noSuggest}
var daysKind = kind{
	name: "days", parse: parseInt, accepts: intAccepts(" of days"), suggest: noSuggest,
	show: func(stored string) string { return stored + "d" },
}

var memoryKind = kind{
	name: "memory",
	parse: func(_ *Key, text string) (string, error) {
		mb, err := utils.ParseMemoryMB(text)
		if err != nil || mb <= 0 {
			return "", fmt.Errorf("%q is not a memory size: use 8g, 16384MB or 8192", text)
		}
		return text, nil
	},
	accepts: func(*Key) string { return "a size such as 8g, 16384MB or 8192 (MB)" },
	suggest: noSuggest,
	show: func(stored string) string {
		mb, _ := utils.ParseMemoryMB(stored)
		return utils.FormatMemoryMB(mb)
	},
}

var walltimeKind = kind{
	name: "walltime",
	parse: func(_ *Key, text string) (string, error) {
		if _, err := utils.ParseWalltime(text); err != nil {
			return "", fmt.Errorf("%q is not a time: use 2h, 01:30:00 or 4d12h", text)
		}
		return text, nil
	},
	accepts: func(*Key) string { return "a time such as 2h, 01:30:00 or 4d12h" },
	suggest: noSuggest,
	show: func(stored string) string {
		d, _ := utils.ParseWalltime(stored)
		return utils.FormatDuration(d)
	},
}

var enumKind = kind{
	name: "enum",
	parse: func(k *Key, text string) (string, error) {
		if text == "" && k.opts.allowEmpty {
			return "", nil
		}
		for _, v := range k.opts.values {
			if strings.EqualFold(v, text) {
				return v, nil
			}
		}
		return "", fmt.Errorf("%q is not one of %s", text, strings.Join(k.opts.values, ", "))
	},
	accepts: func(k *Key) string { return "one of " + strings.Join(k.opts.values, ", ") },
	suggest: func(k *Key) []string { return k.opts.values },
}

var pathKind = kind{
	name: "path",
	parse: func(_ *Key, text string) (string, error) {
		if text == "" {
			return "", errors.New("the path is empty")
		}
		if !filepath.IsAbs(os.ExpandEnv(text)) {
			return "", fmt.Errorf("%q is not an absolute path", text)
		}
		return text, nil
	},
	accepts: func(*Key) string { return "an absolute path, with $VARIABLES expanded when used" },
	suggest: noSuggest,
}

var stringKind = kind{
	name: "string",
	parse: func(k *Key, text string) (string, error) {
		if text == "" && !k.opts.allowEmpty {
			return "", errors.New("the value is empty")
		}
		return text, nil
	},
	accepts: func(*Key) string { return "text" },
	suggest: noSuggest,
}

var listKind = kind{
	name: "list",
	list: true,
	parse: func(_ *Key, text string) (string, error) {
		if text == "" {
			return "", errors.New("the element is empty")
		}
		return text, nil
	},
	accepts: func(*Key) string { return "a list; add elements with config append" },
	suggest: noSuggest,
}

// parseDays reads a stored Days value as a duration.
func parseDays(text string) time.Duration {
	n, _ := strconv.Atoi(text)
	return time.Duration(n) * 24 * time.Hour
}
