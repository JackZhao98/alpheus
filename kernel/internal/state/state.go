// Package state: order lifecycle. Legal transitions only;
// anything else is a bug, not a retry.
package state

import "fmt"

var transitions = map[string]map[string]bool{
	"new":              {"submitted": true, "rejected": true},
	"submitted":        {"partially_filled": true, "filled": true, "cancelled": true, "rejected": true, "expired": true},
	"partially_filled": {"filled": true, "cancelled": true, "expired": true},
}

var Terminal = map[string]bool{"filled": true, "cancelled": true, "rejected": true, "expired": true}

func Advance(current, next string) (string, error) {
	if transitions[current][next] {
		return next, nil
	}
	return current, fmt.Errorf("illegal order transition %s -> %s", current, next)
}
