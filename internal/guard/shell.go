package guard

// Segments splits a shell command line into simple commands, each a list of words with quotes
// removed. It is a reader for the guard, not a shell: it splits on unquoted ; & | newlines,
// parentheses and backticks and at $( (so a command substitution is looked at as its own
// command), honours single and double quotes and backslashes, and expands nothing. Parse
// (parse.go) is the fuller reading both the guard and the risk classifier use.
func Segments(command string) [][]string {
	segments, _ := split(command)
	out := make([][]string, 0, len(segments))
	for _, segment := range segments {
		out = append(out, segment.words)
	}
	return out
}
