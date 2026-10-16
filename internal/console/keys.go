package console

// The key bar's sets (plan/03-shell-fidelity.md 2.4; boards Sqs-02-Pick,
// Sqs-03-Create, Main, Wire-Empty, Wire-Inspect).
//
// A page declares the keys it answers to as data on an element of its own:
//
//	data-keys="j k=queue|⏎=open|/=filter|c=new queue|⌘K=jump or act"
//
// Items are separated by "|", an item is its keycaps (separated by spaces) and
// its words, with "=" between. shell.js draws the bar from the last visible
// declaration in the workspace, so a create form's declaration beats its list
// pane's, and a wire inspector's beats the wire's. Nothing here is per
// service: a service's stage that wants another key adds an item to the
// attribute of its own page, and the key must be one shell.js binds (the table
// KEYBAR there; listpane_keys_test.go compares the two), so a page cannot name
// a key that does nothing.

import "strings"

// keyPalette is the one key every page answers to.
const keyPalette = "⌘K=jump or act"

// sqsCreateKeys is the SQS create page's set (boards Sqs-03-Create and
// Sqs-04-Create-FIFO). Escape leaves the form: the page carries data-esc-back
// beside it.
const sqsCreateKeys = "tab=next field|esc=cancel|" + keyPalette

// createKeys is every other create page's set, as the Create board draws it:
// ⌘⏎ makes the thing, tab moves on, esc leaves with nothing made. ⌘K stays at
// the end, the one key every page answers to (the board is silent on it).
// ⌘⏎ is bound on every create page (shell.js submitCreate), not only on a form
// that holds an editor.
const createKeys = "⌘⏎=create|tab=next field|esc=cancel, nothing made|" + keyPalette

// pluralFor is the heading of a service's list pane. A key the catalogue does
// not know gets the key back, which is at least not blank.
func pluralFor(svc string) string {
	if e, ok := catalogOf(svc); ok && e.Plural != "" {
		return e.Plural
	}
	return svc
}

// itemFor is what one row of a service's list pane is called on the key bar.
func itemFor(svc string) string {
	e, ok := catalogOf(svc)
	if !ok {
		return "item"
	}
	if e.Item != "" {
		return e.Item
	}
	if e.Noun != "" {
		return e.Noun
	}
	return "item"
}

// listKeys is the set of a list pane (board Sqs-02-Pick, with the open key the
// brief adds): move over the rows, open one, filter, make a new one, jump.
func listKeys(svc string) string {
	item := itemFor(svc)
	return strings.Join([]string{"j k=" + item, "⏎=open", "/=filter", "c=new " + item, keyPalette}, "|")
}
