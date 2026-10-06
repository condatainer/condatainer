package settings

import "fmt"

// UnknownKeyError reports a key no package registered.
type UnknownKeyError struct{ Key string }

func (e *UnknownKeyError) Error() string { return fmt.Sprintf("unknown config key: %s", e.Key) }

// FlagConflictError reports two flags that set one key in a single command.
type FlagConflictError struct{ Key, First, Second string }

func (e *FlagConflictError) Error() string {
	return fmt.Sprintf("--%s and --%s both set %s; use one", e.First, e.Second, e.Key)
}
