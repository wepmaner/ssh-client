package sshclient

import "strings"

// ErrorDetail is the error text followed by every wrapped cause it does not already contain:
// the details under a Russian message keep the system text (connectex, lookup, ssh).
func ErrorDetail(err error) string {
	var b strings.Builder
	b.WriteString(err.Error())
	var walk func(error)
	walk = func(e error) {
		var causes []error
		switch u := e.(type) {
		case interface{ Unwrap() []error }:
			causes = u.Unwrap()
		case interface{ Unwrap() error }:
			causes = []error{u.Unwrap()}
		}
		for _, c := range causes {
			if c == nil {
				continue
			}
			if text := c.Error(); !strings.Contains(b.String(), text) {
				b.WriteString("\n" + text)
			}
			walk(c)
		}
	}
	walk(err)
	return b.String()
}

// errorCauses is ErrorDetail without the message itself: what a card shows under the message.
func errorCauses(err error) string {
	return strings.TrimPrefix(strings.TrimPrefix(ErrorDetail(err), err.Error()), "\n")
}
