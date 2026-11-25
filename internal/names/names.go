// Package names formats class and term names the same way eduos-api does.
package names

import "strings"

// Class turns a level and arm into a class name, e.g. JSS_2 + A = "JSS 2A", PRIMARY_3 + Gold = "Primary 3 Gold".
func Class(level, arm string) string {
	parts := strings.SplitN(level, "_", 2)
	label := level
	if len(parts) == 2 {
		if parts[0] == "PRIMARY" {
			label = "Primary " + parts[1]
		} else {
			label = parts[0] + " " + parts[1]
		}
	}
	if len(arm) == 1 {
		return label + arm
	}
	return label + " " + arm
}

func Term(n int) string {
	switch n {
	case 1:
		return "First term"
	case 2:
		return "Second term"
	case 3:
		return "Third term"
	}
	return "Term"
}
