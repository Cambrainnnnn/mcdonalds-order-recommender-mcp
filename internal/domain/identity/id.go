package identity

import "strconv"

// Valid reports whether value is a canonical positive signed 64-bit decimal ID.
// IDs travel as strings so JavaScript clients never round them.
func Valid(value string) bool {
	n, err := strconv.ParseInt(value, 10, 64)
	return err == nil && n > 0 && strconv.FormatInt(n, 10) == value
}
