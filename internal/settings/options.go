package settings

import "fmt"

type options struct {
	defaultText string
	defaultList []string
	defaultFn   func() any
	validate    func(string) error
	accepts     string
	merge       bool
	aliases     []Alias
	deprecated  *Deprecation
	omitEmpty   bool
	split       func(string) []string
	help        string
	min, max    *int
	values      []string
	allowEmpty  bool
	normalize   func(string) string
	show        func(string) string
	note        func(string) string
	suggest     []string
	check       func(string) error
	detect      func() string
	order       int
}

// Option adjusts a declaration.
type Option func(*options)

// Default sets the default. It is formatted as text and parsed by the key's kind.
func Default(v any) Option { return func(o *options) { o.defaultText = fmt.Sprint(v) } }

// DefaultFunc computes the default each time it is read, for a default that depends on the
// environment at that moment, such as HOME.
func DefaultFunc(fn func() any) Option { return func(o *options) { o.defaultFn = fn } }

// MergeLists makes a list key the union of every layer's list, highest layer first and each element once.
// Without it the highest layer that sets the list wins. A flag or an environment variable still replaces it.
func MergeLists() Option { return func(o *options) { o.merge = true } }

// OmitEmpty hides the key in `config list` while its value is empty.
func OmitEmpty() Option { return func(o *options) { o.omitEmpty = true } }

// Accepts describes the values the key takes, when the kind's description is too general.
func Accepts(text string) Option { return func(o *options) { o.accepts = text } }

// Validate rejects a value the key's kind accepts but that is not valid for this key.
func Validate(fn func(text string) error) Option { return func(o *options) { o.validate = fn } }

// Split turns the text of an environment variable into list elements.
// The default splits on "|", or on ":" when there is no "|".
func Split(fn func(string) []string) Option { return func(o *options) { o.split = fn } }

// DefaultList sets the default of a List key.
func DefaultList(v ...string) Option { return func(o *options) { o.defaultList = v } }

// Help is the one line that says what the key does. It is required.
func Help(text string) Option { return func(o *options) { o.help = text } }

// Min is the smallest integer an Int or Days key takes.
func Min(n int) Option { return func(o *options) { o.min = &n } }

// Max is the largest integer an Int key takes.
func Max(n int) Option { return func(o *options) { o.max = &n } }

// Values lists what an Enum key takes. The first spelling of each is the stored one.
func Values(values ...string) Option { return func(o *options) { o.values = values } }

// AllowEmpty makes an empty file value mean something instead of unset.
func AllowEmpty() Option { return func(o *options) { o.allowEmpty = true } }

// Normalize rewrites a parsed value into the form that is stored.
func Normalize(fn func(string) string) Option { return func(o *options) { o.normalize = fn } }

// Show formats a stored value for `config list`.
func Show(fn func(stored string) string) Option { return func(o *options) { o.show = fn } }

// Note adds a line under the value in `config list`.
func Note(fn func(stored string) string) Option { return func(o *options) { o.note = fn } }

// Suggest adds completion values beyond what the kind gives.
func Suggest(values ...string) Option { return func(o *options) { o.suggest = values } }

// Check rejects a value that is not true of the machine, such as a binary that does not exist.
func Check(fn func(stored string) error) Option { return func(o *options) { o.check = fn } }

// Detect makes `config init` write the value fn finds.
func Detect(fn func() string) Option { return func(o *options) { o.detect = fn } }

// Order places the key in a listing, lowest first, before the alphabetical rest.
func Order(n int) Option { return func(o *options) { o.order = n } }
