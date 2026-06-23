package v1

// Logger is the SDK-published logging surface. The host implementation
// stamps an `extension=<id>` field on every entry so logs from different
// extensions are filterable in the unified zerolog stream.
//
// The interface is intentionally tiny — four severity methods, each taking
// a pre-formatted string. Structured fields (zerolog's chained `.Err()` /
// `.Str()` builders) are not exposed; if a Logger consumer needs structured
// data, format it into the message string with `fmt.Sprintf`. This keeps
// the SDK surface stable across logging backends and prevents extensions
// from depending on zerolog directly.
type Logger interface {
	Debug(msg string)
	Info(msg string)
	Warn(msg string)
	Error(msg string)
}
