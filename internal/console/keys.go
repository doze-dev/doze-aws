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

// createKeys is every create page's set, exactly as the Create board draws it:
// ⌘⏎ makes the thing, tab moves on, esc leaves with nothing made. It does not
// name ⌘K. The owner decided (2026-10-20) that the bar does what the Create
// board draws, and the board has no ⌘K; the key still opens the palette here,
// the bar just does not say so. (Sqs-03 and the other services' create boards
// draw "⌘K jump or act" on this bar; the Create board wins.)
// ⌘⏎ is bound on every create page (shell.js submitCreate), not only on a form
// that holds an editor, and Escape leaves it (data-esc-back beside the keys).
const createKeys = "⌘⏎=create|tab=next field|esc=cancel, nothing made"

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

// The queue page's sets, one per state, as the boards draw them (Queue,
// Sqs-08, Sqs-10, Sqs-17, Sqs-18, Sqs-19). A state declares its own from its
// template (data-keys), and the bar draws the last visible declaration, so the
// composer and the edit form, which sit inside the page, replace the page's.
//
// "⏎ open a message" (Queue) is not named: there is no message cursor, so ⏎ on
// this page would open nothing. ⌘⏎ is on the Messages tab because it opens the
// composer there and sends from it (shell.js, KEYBAR).
const (
	sqsMessagesKeys = "j k=queue|⌘⏎=send|/=filter|" + keyPalette
	sqsComposeKeys  = "⌘⏎=send|esc=close|" + keyPalette
	sqsConsumeKeys  = "⏎=receive|" + keyPalette
	sqsConfigKeys   = "j k=queue|" + keyPalette
	sqsEditKeys     = "tab=next field|esc=cancel|" + keyPalette
	sqsTagsKeys     = "tab=next field|" + keyPalette
)

// sqsKeys is the set of one tab of a queue's page.
func sqsKeys(tab string) string {
	switch tab {
	case "consume":
		return sqsConsumeKeys
	case "config":
		return sqsConfigKeys
	case "tags":
		return sqsTagsKeys
	}
	return sqsMessagesKeys
}

// sqsNarrowKeys is a queue tab's bar at 1100 px and below, where board Narrow
// draws "j k message": the pane is a switcher, so the keys move over the
// messages the tab shows (shell.js msgMove). shell.js uses it only while the tab
// has message rows; a tab with none has no such set.
func sqsNarrowKeys(tab string) string {
	switch tab {
	case "", "messages", "consume":
		return "j k=message"
	}
	return ""
}

// emptyListKeys is the set of a list pane with nothing in it (board
// Sqs-01-None): there is no row to move over, open or filter, so only the key
// that makes the first one (when the service has a create) and the palette.
func emptyListKeys(svc string, canNew bool) string {
	if !canNew {
		return keyPalette
	}
	return "c=new " + itemFor(svc) + "|" + keyPalette
}
