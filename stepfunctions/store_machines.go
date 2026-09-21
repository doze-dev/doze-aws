package stepfunctions

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"

	"github.com/doze-dev/doze-aws/internal/awshttp"
)

// State machine and activity accessors.

// PutMachine stores a state machine, refusing a duplicate name.
//
// CreateStateMachine is not idempotent in AWS the way PutRule is: creating one
// that exists is StateMachineAlreadyExists unless the definition, role and type
// are all identical, in which case AWS returns the existing one. That exception
// is what makes `cdk deploy` of an unchanged stack succeed instead of erroring,
// so it is not a detail to skip.
func (s *store) PutMachine(m *stateMachine) (*stateMachine, *awshttp.APIError) {
	var existing stateMachine
	found, err := s.get(bucketMachines, []byte(m.Name), &existing)
	if err != nil {
		return nil, asAPIError(err)
	}
	if found {
		if existing.Definition == m.Definition && existing.RoleARN == m.RoleARN && existing.Type == m.Type {
			return &existing, nil
		}
		return nil, errMachineExists(existing.ARN)
	}
	m.CreatedAt = s.now()
	m.Status = "ACTIVE"
	m.RevisionID = revisionOf(m.Definition, m.RoleARN)
	if err := s.put(bucketMachines, []byte(m.Name), m); err != nil {
		return nil, asAPIError(err)
	}
	return m, nil
}

// GetMachine reads one by name.
func (s *store) GetMachine(name string) (*stateMachine, *awshttp.APIError) {
	var m stateMachine
	found, err := s.get(bucketMachines, []byte(name), &m)
	if err != nil {
		return nil, asAPIError(err)
	}
	if !found {
		return nil, nil
	}
	return &m, nil
}

// UpdateMachine applies the fields UpdateStateMachine may change. A field left
// nil is untouched, which is the operation's contract — passing only RoleArn
// must not blank the definition.
// machineConfigs carries the configuration blocks an update may replace;
// a nil block is "not sent".
type machineConfigs struct {
	Logging, Tracing, Encryption json.RawMessage
}

func (s *store) UpdateMachine(name string, definition, roleARN *string, configs machineConfigs) (*stateMachine, *awshttp.APIError) {
	m, aerr := s.GetMachine(name)
	if aerr != nil {
		return nil, aerr
	}
	if m == nil {
		return nil, nil
	}
	if definition != nil {
		m.Definition = *definition
	}
	if roleARN != nil {
		m.RoleARN = *roleARN
	}
	// The configuration blocks replace what was stored when sent; UpdateStateMachine
	// is a full replacement of each block on AWS too.
	if configs.Logging != nil {
		m.LoggingConfiguration = configs.Logging
	}
	if configs.Tracing != nil {
		m.TracingConfiguration = configs.Tracing
	}
	if configs.Encryption != nil {
		m.EncryptionConfiguration = configs.Encryption
	}
	// The revision changes only when something that affects execution does.
	// CDK reads stateMachineRevisionId back and would see a phantom change if
	// every update bumped it.
	if rev := revisionOf(m.Definition, m.RoleARN); rev != m.RevisionID {
		m.RevisionID = rev
	}
	if err := s.put(bucketMachines, []byte(name), m); err != nil {
		return nil, asAPIError(err)
	}
	return m, nil
}

// DeleteMachine removes a machine. AWS makes this asynchronous — the machine
// enters DELETING and disappears once its executions finish — but locally there
// is nothing to drain in stage 1, so it is immediate. Documented as a
// difference rather than hidden, because a caller polling for DELETING would
// otherwise loop forever.
func (s *store) DeleteMachine(name string) *awshttp.APIError {
	return asAPIError(s.delete(bucketMachines, []byte(name)))
}

// ListMachines returns every machine, in name order (bbolt's cursor order).
func (s *store) ListMachines() ([]stateMachine, *awshttp.APIError) {
	var out []stateMachine
	err := s.each(bucketMachines, func(_, raw []byte) error {
		var m stateMachine
		if err := json.Unmarshal(raw, &m); err != nil {
			return err
		}
		out = append(out, m)
		return nil
	})
	if err != nil {
		return nil, asAPIError(err)
	}
	return out, nil
}

// PutActivity stores an activity, refusing a duplicate.
func (s *store) PutActivity(a *activity) (*activity, *awshttp.APIError) {
	var existing activity
	found, err := s.get(bucketActivities, []byte(a.Name), &existing)
	if err != nil {
		return nil, asAPIError(err)
	}
	if found {
		// AWS returns the existing activity rather than erroring, because an
		// activity has no mutable state to conflict over.
		return &existing, nil
	}
	a.CreatedAt = s.now()
	if err := s.put(bucketActivities, []byte(a.Name), a); err != nil {
		return nil, asAPIError(err)
	}
	return a, nil
}

func (s *store) GetActivity(name string) (*activity, *awshttp.APIError) {
	var a activity
	found, err := s.get(bucketActivities, []byte(name), &a)
	if err != nil {
		return nil, asAPIError(err)
	}
	if !found {
		return nil, nil
	}
	return &a, nil
}

func (s *store) DeleteActivity(name string) *awshttp.APIError {
	return asAPIError(s.delete(bucketActivities, []byte(name)))
}

func (s *store) ListActivities() ([]activity, *awshttp.APIError) {
	var out []activity
	err := s.each(bucketActivities, func(_, raw []byte) error {
		var a activity
		if err := json.Unmarshal(raw, &a); err != nil {
			return err
		}
		out = append(out, a)
		return nil
	})
	if err != nil {
		return nil, asAPIError(err)
	}
	return out, nil
}

// revisionOf is the stateMachineRevisionId: a stable digest of what actually
// affects execution. Deriving it rather than counting updates means an update
// that changes nothing reports no new revision, which is what CDK expects.
func revisionOf(definition, roleARN string) string {
	sum := sha256.Sum256([]byte(definition + "\x00" + roleARN))
	return hex.EncodeToString(sum[:8])
}
