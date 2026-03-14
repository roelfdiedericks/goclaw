package runtime

// ShellQuote properly quotes a string for shell use.
func ShellQuote(s string) string {
	out := "'"
	for _, c := range s {
		if c == '\'' {
			out += "'\\''"
		} else {
			out += string(c)
		}
	}
	out += "'"
	return out
}
