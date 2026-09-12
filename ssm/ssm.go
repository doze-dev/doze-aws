// Package ssm is doze-aws's local Systems Manager Parameter Store: parameter
// hierarchies with versions, labels, and history; String, StringList, and
// SecureString types. SecureString values are genuinely encrypted at rest with
// a per-data-dir AES-256-GCM key that the service manages itself (a KMS KeyId
// is recorded and returned cosmetically) — so SSM works with or without the
// kms service enabled.
//
// Only the Parameter Store slice of SSM's huge API is meaningful locally.
// Fleet management (documents, Run Command, sessions, patching, inventory,
// OpsCenter, maintenance windows) needs managed instances that don't exist
// here; those operations answer a clean UnsupportedOperationException.
//
// See docs/api-support/ssm.md for the operation-by-operation support table.
package ssm

import (
	"net/http"
	"os"
	"path/filepath"
	"time"

	bolt "go.etcd.io/bbolt"

	"github.com/doze-dev/doze-aws/awsident"
	"github.com/doze-dev/doze-aws/internal/schemaver"

	"github.com/doze-dev/doze-aws/internal/awshttp"
	"github.com/doze-dev/doze-aws/internal/awsjson"
	"github.com/doze-dev/doze-aws/internal/bg"
	"github.com/doze-dev/doze-aws/internal/modelcheck"
	"github.com/doze-dev/doze-aws/peers"
)

// Options configures the service.
type Options struct {
	// DataDir holds the bbolt store (ssm.bolt) and the SecureString key
	// (ssm.key). Required.
	DataDir string
	// Peers is accepted for constructor uniformity; SSM calls no siblings.
	Peers peers.Directory
	// Logf receives log lines; nil discards.
	Logf func(format string, args ...any)
	// Clock overrides time.Now in tests.
	Clock func() time.Time
	// Identity is the region and account this service mints ARNs for. The zero
	// value means the conventional local identity.
	Identity awsident.Identity
}

// Server is the SSM service: an http.Handler speaking AWS JSON 1.1, and an
// io.Closer that stops the janitor and closes the store.
type Server struct {
	store *Store
	logf  func(format string, args ...any)
	api   awsjson.API
	stop  chan struct{}
	// done closes when the janitor has returned, so Close waits for it before
	// closing bbolt — a sweep mid-transaction against a closed DB is a panic.
	done chan struct{}
	id   awsident.Identity // the region and account this service mints ARNs for
}

// New opens the store under DataDir and starts the expiration janitor.
func New(opts Options) (*Server, error) {
	if err := os.MkdirAll(opts.DataDir, 0o755); err != nil {
		return nil, err
	}
	db, err := bolt.Open(filepath.Join(opts.DataDir, "ssm.bolt"), 0o600, nil)
	if err != nil {
		return nil, err
	}
	if err := schemaver.Ensure(db, "ssm", schemaver.Current); err != nil {
		db.Close()
		return nil, err
	}
	st, err := newStore(db, filepath.Join(opts.DataDir, "ssm.key"))
	if err != nil {
		db.Close()
		return nil, err
	}
	logf := opts.Logf
	if logf == nil {
		logf = func(string, ...any) {}
	}
	s := &Server{
		store: st,
		logf:  logf,
		api:   awsjson.API{TargetPrefix: "AmazonSSM", JSONVersion: "1.1"},
		stop:  make(chan struct{}),
		done:  make(chan struct{}),
		id:    opts.Identity,
	}
	if opts.Clock != nil {
		s.store.clock = opts.Clock
	}
	go s.janitor()
	return s, nil
}

// Close stops the janitor and closes the bbolt DB.
func (s *Server) Close() error {
	close(s.stop)
	// Waited on, not just signalled: a sweep inside a bolt transaction races
	// the close below, and bolt panics on a closed DB.
	<-s.done
	return s.store.db.Close()
}

// janitor enforces parameter Expiration policies.
func (s *Server) janitor() {
	defer close(s.done)
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for {
		select {
		case <-s.stop:
			return
		case <-t.C:
			bg.Tick(s.logf, "ssm: janitor", func() { s.store.SweepExpired() })
		}
	}
}

type handler func(s *Server, p map[string]any) (any, *awshttp.APIError)

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	action, aerr := s.api.Action(r)
	if aerr != nil {
		s.api.WriteError(w, aerr)
		return
	}
	var params map[string]any
	if aerr := awsjson.DecodeBody(r, &params); aerr != nil {
		s.api.WriteError(w, aerr)
		return
	}
	h, ok := handlers[action]
	if !ok {
		if fleetOps[action] {
			s.api.WriteError(w, awshttp.Errf(400, "UnsupportedOperationException",
				"%s is not supported by doze-aws: it needs managed instances/fleet infrastructure that does not exist locally", action))
			return
		}
		s.api.WriteError(w, awshttp.Errf(400, "InvalidAction", "unknown SSM action %q", action))
		return
	}
	// Model-derived input validation runs before the handler, for every
	// operation at once — coverage is then a property of the dispatch table
	// rather than something each handler has to remember.
	if aerr := modelcheck.ValidateMap(params, constraintTables[action]); aerr != nil {
		s.api.WriteError(w, aerr)
		return
	}

	result, aerr := h(s, params)
	if aerr != nil {
		s.logf("ssm: %s -> %s", action, aerr.Code)
		s.api.WriteError(w, aerr)
		return
	}
	s.logf("ssm: %s ok", action)
	s.api.Write(w, result)
}

// fleetOps names the SSM surface that cannot exist locally (Tier S). The list
// is prefix-matched conceptually but enumerated for exactness in api-support.
var fleetOps = map[string]bool{}

func init() {
	for _, name := range []string{
		// Documents & automation.
		"CreateDocument", "DeleteDocument", "DescribeDocument", "GetDocument", "ListDocuments",
		"UpdateDocument", "UpdateDocumentDefaultVersion", "UpdateDocumentMetadata",
		"ListDocumentVersions", "DescribeDocumentPermission", "ModifyDocumentPermission",
		"StartAutomationExecution", "StopAutomationExecution", "GetAutomationExecution",
		"DescribeAutomationExecutions", "DescribeAutomationStepExecutions", "SendAutomationSignal",
		// Run Command.
		"SendCommand", "CancelCommand", "ListCommands", "ListCommandInvocations", "GetCommandInvocation",
		// Sessions.
		"StartSession", "ResumeSession", "TerminateSession", "DescribeSessions",
		// Instances / fleet.
		"DescribeInstanceInformation", "DescribeInstanceAssociationsStatus", "DescribeInstanceProperties",
		"CreateActivation", "DeleteActivation", "DescribeActivations", "DeregisterManagedInstance",
		"UpdateManagedInstanceRole",
		// Associations.
		"CreateAssociation", "CreateAssociationBatch", "DeleteAssociation", "DescribeAssociation",
		"ListAssociations", "UpdateAssociation", "UpdateAssociationStatus", "StartAssociationsOnce",
		"ListAssociationVersions", "DescribeEffectiveInstanceAssociations",
		// Patching & inventory & compliance.
		"CreatePatchBaseline", "DeletePatchBaseline", "DescribePatchBaselines", "GetPatchBaseline",
		"UpdatePatchBaseline", "RegisterPatchBaselineForPatchGroup", "DeregisterPatchBaselineForPatchGroup",
		"DescribePatchGroups", "DescribeInstancePatches", "DescribeInstancePatchStates",
		"PutInventory", "GetInventory", "GetInventorySchema", "DeleteInventory", "ListInventoryEntries",
		"PutComplianceItems", "ListComplianceItems", "ListComplianceSummaries", "ListResourceComplianceSummaries",
		// Maintenance windows.
		"CreateMaintenanceWindow", "DeleteMaintenanceWindow", "DescribeMaintenanceWindows",
		"GetMaintenanceWindow", "UpdateMaintenanceWindow", "RegisterTargetWithMaintenanceWindow",
		"RegisterTaskWithMaintenanceWindow", "DeregisterTargetFromMaintenanceWindow",
		"DeregisterTaskFromMaintenanceWindow", "DescribeMaintenanceWindowExecutions",
		// OpsCenter / OpsItems.
		"CreateOpsItem", "GetOpsItem", "DescribeOpsItems", "UpdateOpsItem", "CreateOpsMetadata",
		"GetOpsMetadata", "ListOpsMetadata", "UpdateOpsMetadata", "DeleteOpsMetadata", "GetOpsSummary",
		// Resource data sync / service settings.
		"CreateResourceDataSync", "DeleteResourceDataSync", "ListResourceDataSync", "UpdateResourceDataSync",
		"GetServiceSetting", "ResetServiceSetting", "UpdateServiceSetting",
	} {
		fleetOps[name] = true
	}
}
