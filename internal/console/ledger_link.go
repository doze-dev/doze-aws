package console

// A2, the links under a refusal's explanation.
//
// "Refused here" says AWS would accept the call and doze-aws will not, and "Not
// built" says the operation is not here. Both are claims about one operation,
// and the ledger (docs/SUPPORT.md, which the fidelity dialog draws) is where
// that operation's row says what is real about it and why. So each of the two
// explanations carries a link to that row: the fidelity dialog, opened scrolled
// to the operation and with its row marked (?op= on /info/{svc}, the anchors in
// info_panel). A not-built operation also links docs/not-built.md, which says
// which kind of absence it is.
//
// The link is a plain <a href="/{svc}?fidelity=Op"> that shell.js turns into
// "open this page's dialog at that row" when the page has one for the service
// (the wire's inspector does, and so does every service's own page), and that,
// followed, opens the service's page and then its dialog at the row.

import (
	"io"
	"net/http"
	"net/url"
	"strings"
)

// notBuiltDoc is where docs/not-built.md is read. The console does not serve
// the repository's docs (only the ledger is embedded, because the dialog draws
// it), so this is the file on the project's own repository.
const notBuiltDoc = "https://github.com/doze-dev/doze-aws/blob/main/docs/not-built.md"

// ledgerRef is a link to one operation's row in the ledger.
type ledgerRef struct {
	Svc, Op string
	Text    string // "The ledger row for ListQueueTags →"
	Path    string // prefix-relative: "/sqs?fidelity=ListQueueTags"
}

// ledgerRefFor is the link for a failure of the given kind on an operation of a
// console service, or nil when the kind does not point at the ledger (only
// Refused here and Not built do), when the call names no service the console has
// a page for, or when the stack simply lacks the service (a limit of the
// stack, not of an operation).
func ledgerRefFor(state, detail, svc, op string) *ledgerRef {
	if state != "refusedhere" && state != "notbuilt" {
		return nil
	}
	if state == "refusedhere" && detail == serviceNotRunning {
		return nil
	}
	e, ok := catalogOf(svc)
	if !ok {
		return nil
	}
	// A row only when the ledger names the operation: a link to a row that is not
	// there would open the dialog and mark nothing.
	if op != "" && ledgerFor(svc, op) == nil {
		op = ""
	}
	// ?fidelity= opens the service's dialog; with an operation it opens at its row.
	r := &ledgerRef{Svc: svc, Op: op, Path: "/" + svc + "?fidelity=" + url.QueryEscape(op)}
	if op == "" {
		r.Text = "The " + e.Label + " ledger →"
	} else {
		r.Text = "The ledger row for " + op + " →"
	}
	return r
}

// notBuiltRef is a link to docs/not-built.md.
type notBuiltRef struct {
	Text, Href string
}

// notBuiltRefFor is the link for a not-built operation: the document's section
// for what the stamp says (declined or deferred), and its top for a legacy stub
// that does not say.
func notBuiltRefFor(state, detail string) *notBuiltRef {
	if state != "notbuilt" {
		return nil
	}
	switch detail {
	case "declined":
		return &notBuiltRef{Text: "Why it is declined: docs/not-built.md →", Href: notBuiltDoc + "#declined"}
	case "deferred":
		return &notBuiltRef{Text: "What it would take: docs/not-built.md →", Href: notBuiltDoc + "#not-yet"}
	}
	return &notBuiltRef{Text: "What is not built, and why: docs/not-built.md →", Href: notBuiltDoc}
}

// operationOf is the operation a request the console made addresses, as far as
// the request says: the target header of a JSON service, the Action of a Query
// one. A path-routed service (S3, Lambda, API Gateway) names its operation
// nowhere in the request, so the answer is empty and the link goes to the
// service's ledger, not to a row it would have to guess.
func operationOf(req *http.Request) string {
	if t := req.Header.Get("X-Amz-Target"); t != "" {
		if _, act, ok := strings.Cut(t, "."); ok {
			return act
		}
	}
	if strings.Contains(req.Header.Get("Content-Type"), "x-www-form-urlencoded") && req.GetBody != nil {
		if rc, err := req.GetBody(); err == nil {
			defer rc.Close()
			if b, err := io.ReadAll(io.LimitReader(rc, 8192)); err == nil {
				if vals, err := url.ParseQuery(string(b)); err == nil {
					return vals.Get("Action")
				}
			}
		}
	}
	return ""
}
