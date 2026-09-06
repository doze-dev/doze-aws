package stepfunctions

// Task tokens: one row per outstanding token, written and deleted in the
// same transaction as the execution record (SaveTransition), because the two
// travel together — a PARKED frame with no redeemable token is a hung
// execution, and a redeemable token whose result was already applied is a
// double delivery.

var bucketTokens = []byte("tokens")

// TokenRef locates the frame a token redeems.
type TokenRef struct {
	ExecKey  string `json:"exec_key"`
	Frame    int    `json:"frame"`
	IssuedAt int64  `json:"issued_at"`
}

// tokenOp is one buffered token write: Ref nil means delete.
type tokenOp struct {
	Token string
	Ref   *TokenRef
}

// GetToken resolves a token, nil when unknown (never issued, already
// redeemed, or timed out).
func (s *Store) GetToken(token string) (*TokenRef, error) {
	var ref TokenRef
	found, err := s.get(bucketTokens, []byte(token), &ref)
	if err != nil || !found {
		return nil, err
	}
	return &ref, nil
}
