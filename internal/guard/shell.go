package guard

import "strings"

// Segments splits a shell command line into simple commands, each a list of words with quotes
// removed. It is a reader for the guard, not a shell: it splits on unquoted ; & | newlines,
// parentheses and backticks and at $( (so a command substitution is looked at as its own
// command), honours single and double quotes and backslashes, and expands nothing.
func Segments(command string) [][]string {
	var (
		segments [][]string
		words    []string
		word     strings.Builder
		inWord   bool
		quote    rune
		// resume holds the quote to return to at each ) that closes a $( opened inside double
		// quotes, so the substitution's words are split like any command's.
		resume []rune
	)
	endWord := func() {
		if inWord {
			words = append(words, word.String())
			word.Reset()
			inWord = false
		}
	}
	endSegment := func() {
		endWord()
		if len(words) > 0 {
			segments = append(segments, words)
			words = nil
		}
	}
	runes := []rune(command)
	for i := 0; i < len(runes); i++ {
		r := runes[i]
		switch {
		case quote == '\'':
			if r == '\'' {
				quote = 0
			} else {
				word.WriteRune(r)
			}
		case quote == '"':
			switch {
			case r == '"':
				quote = 0
			case r == '\\' && i+1 < len(runes) && strings.ContainsRune("\"\\$`\n", runes[i+1]):
				i++
				word.WriteRune(runes[i])
			case r == '$' && i+1 < len(runes) && runes[i+1] == '(':
				// A substitution inside double quotes is a command of its own.
				endSegment()
				i++
				resume = append(resume, '"')
				quote = 0
			case r == '`':
				endSegment()
			default:
				word.WriteRune(r)
			}
		case r == '\'' || r == '"':
			quote = r
			inWord = true
		case r == '\\' && i+1 < len(runes):
			i++
			if runes[i] != '\n' {
				word.WriteRune(runes[i])
				inWord = true
			}
		case r == '$' && i+1 < len(runes) && runes[i+1] == '(':
			endSegment()
			i++
			resume = append(resume, 0)
		case r == ')' && len(resume) > 0:
			endSegment()
			quote = resume[len(resume)-1]
			resume = resume[:len(resume)-1]
		case strings.ContainsRune(";&|\n()`", r):
			endSegment()
		case r == ' ' || r == '\t' || r == '\r':
			endWord()
		default:
			word.WriteRune(r)
			inWord = true
		}
	}
	endSegment()
	return segments
}
