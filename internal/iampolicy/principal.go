package iampolicy

// Principal matching, for resource policies. A statement with no Principal
// block (an identity policy) matches every caller; one with a Principal
// matches when the caller is named by it; NotPrincipal inverts that.
//
// The forms AWS accepts, as they match locally:
//
//	"*" and {"AWS": "*"}                 anyone, including anonymous
//	{"AWS": "arn:aws:iam::acct:root"}    the account: root itself, and a delegation
//	                                     to the identity policies of its users
//	{"AWS": "acct"}                      the same, by account id
//	{"AWS": "arn:aws:iam::acct:user/x"}  that user; roles likewise
//	{"Service": "s3.amazonaws.com"}      a service principal, when a service calls
//	{"AWS": ["a", "b"]}                  any of them

import (
	"strings"

	"github.com/doze-dev/doze-aws/awsident"
)

// principalMatches reports whether a statement's Principal (or NotPrincipal)
// admits the caller. An empty caller with a Principal block present matches
// only "*".
func principalMatches(st *Statement, caller string) bool {
	if st.NotPrincipal.Present {
		return !principalNamed(st.NotPrincipal, caller)
	}
	if !st.Principal.Present {
		return true
	}
	return principalNamed(st.Principal, caller)
}

// principalNamed reports whether a principal block names the caller.
func principalNamed(p principalBlock, caller string) bool {
	if p.Any {
		return true
	}
	if caller == "" {
		return false
	}
	if strings.HasSuffix(caller, ".amazonaws.com") {
		for _, svc := range p.ByType["Service"] {
			if strings.EqualFold(svc, caller) {
				return true
			}
		}
		return false
	}
	rootARN := awsident.GlobalARN("iam", "root")
	for _, aws := range p.ByType["AWS"] {
		switch {
		case aws == caller:
			return true
		case aws == "*":
			return true
		case aws == rootARN || aws == awsident.AccountID:
			// The account principal names the account, not its identities: it
			// admits the root caller itself and delegates to the identity
			// policies of the account's users, which the same-account rule
			// already honours. Naming a user grants that user directly.
			if caller == rootARN {
				return true
			}
		case strings.Contains(aws, "*"):
			if globMatch(aws, caller) {
				return true
			}
		}
	}
	return false
}
