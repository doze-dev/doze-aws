package iam

// The bbolt-backed store: principals (users, groups, roles), managed policies
// with versions, access keys, and instance profiles.
//
// Access keys are indexed by key id rather than hung off the user record,
// because principal resolution runs on every request in enforce mode and must
// be a single point lookup.

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	bolt "go.etcd.io/bbolt"

	"github.com/doze-dev/doze-aws/awsident"
)

var (
	userBucket    = []byte("users")
	groupBucket   = []byte("groups")
	roleBucket    = []byte("roles")
	policyBucket  = []byte("policies")
	keyBucket     = []byte("accesskeys")
	profileBucket = []byte("instanceprofiles")
	accountBucket = []byte("account")
)

// maxPolicyVersions matches AWS: a managed policy holds at most five versions,
// and the sixth CreatePolicyVersion fails until one is deleted.
const maxPolicyVersions = 5

// Principal is the behaviour users, groups and roles share: an identity that
// can carry inline and attached policies.
type Principal struct {
	Name                string            `json:"name"`
	Path                string            `json:"path"`
	ID                  string            `json:"id"`
	Created             int64             `json:"created"`
	Tags                map[string]string `json:"tags,omitempty"`
	Inline              map[string]string `json:"inline,omitempty"`   // policy name -> document
	Attached            []string          `json:"attached,omitempty"` // policy ARNs
	PermissionsBoundary string            `json:"boundary,omitempty"`
}

// User is an IAM user.
type User struct {
	Principal
	Groups []string `json:"groups,omitempty"`
}

// Group is an IAM group.
type Group struct {
	Principal
}

// Role is an IAM role. AssumeRolePolicy is the trust policy; it is stored and
// returned faithfully, and STS consults it when a role is assumed.
type Role struct {
	Principal
	AssumeRolePolicy   string `json:"assume_role_policy,omitempty"`
	Description        string `json:"description,omitempty"`
	MaxSessionDuration int    `json:"max_session_duration,omitempty"`
}

// Policy is a customer-managed policy with its version history.
type Policy struct {
	Name           string            `json:"name"`
	Path           string            `json:"path"`
	ID             string            `json:"id"`
	Created        int64             `json:"created"`
	Updated        int64             `json:"updated"`
	Description    string            `json:"description,omitempty"`
	DefaultVersion string            `json:"default_version"`
	Versions       map[string]string `json:"versions"` // v1 -> document
	VersionOrder   []string          `json:"version_order"`
	AttachCount    int               `json:"attach_count"`
	Tags           map[string]string `json:"tags,omitempty"`
}

// ARN is the policy's customer-managed ARN.
func (p *Policy) ARN() string { return awsident.GlobalARN("iam", "policy"+p.Path+p.Name) }

// Default returns the document of the default version.
func (p *Policy) Default() string { return p.Versions[p.DefaultVersion] }

// AccessKey is a credential pair bound to a user. The secret is stored because
// a local emulator has to hand it back; it authenticates nothing.
type AccessKey struct {
	ID       string `json:"id"`
	Secret   string `json:"secret"`
	UserName string `json:"user"`
	Status   string `json:"status"` // Active | Inactive
	Created  int64  `json:"created"`
	LastUsed int64  `json:"last_used,omitempty"`
	LastSvc  string `json:"last_service,omitempty"`
}

// InstanceProfile wraps a role; CloudFormation templates create them freely.
type InstanceProfile struct {
	Name    string            `json:"name"`
	Path    string            `json:"path"`
	ID      string            `json:"id"`
	Created int64             `json:"created"`
	Roles   []string          `json:"roles,omitempty"`
	Tags    map[string]string `json:"tags,omitempty"`
}

// Store is the bbolt-backed IAM state.
type Store struct {
	db    *bolt.DB
	clock func() time.Time
}

func newStore(db *bolt.DB) *Store { return &Store{db: db, clock: time.Now} }

func (s *Store) now() time.Time { return s.clock() }

// ---- generic helpers ----

func getJSON[T any](tx *bolt.Tx, bucket []byte, key string) (*T, bool) {
	b := tx.Bucket(bucket)
	if b == nil {
		return nil, false
	}
	raw := b.Get([]byte(key))
	if raw == nil {
		return nil, false
	}
	var v T
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil, false
	}
	return &v, true
}

func putJSON(tx *bolt.Tx, bucket []byte, key string, v any) error {
	b, err := tx.CreateBucketIfNotExists(bucket)
	if err != nil {
		return err
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return b.Put([]byte(key), raw)
}

func listJSON[T any](tx *bolt.Tx, bucket []byte) []T {
	var out []T
	b := tx.Bucket(bucket)
	if b == nil {
		return out
	}
	_ = b.ForEach(func(_, raw []byte) error {
		var v T
		if json.Unmarshal(raw, &v) == nil {
			out = append(out, v)
		}
		return nil
	})
	return out
}

// normPath defaults an IAM path to "/" and enforces the leading/trailing slash
// rule, so ARNs built from it are always well formed.
func normPath(p string) string {
	if p == "" {
		return "/"
	}
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	if !strings.HasSuffix(p, "/") {
		p += "/"
	}
	return p
}

// uniqueID generates an IAM-shaped unique id: a fixed type prefix plus 17
// uppercase alphanumerics.
func uniqueID(prefix string) string {
	const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZ234567"
	b := make([]byte, 17)
	_, _ = rand.Read(b)
	for i := range b {
		b[i] = alphabet[int(b[i])%len(alphabet)]
	}
	return prefix + string(b)
}

// ---- users ----

func (s *Store) CreateUser(name, path string, tags map[string]string) (*User, error) {
	if err := validName("UserName", name); err != nil {
		return nil, err
	}
	var out *User
	err := s.db.Update(func(tx *bolt.Tx) error {
		if _, ok := getJSON[User](tx, userBucket, name); ok {
			return errEntityExists("User with name %s already exists.", name)
		}
		u := &User{Principal: Principal{
			Name: name, Path: normPath(path), ID: uniqueID("AIDA"),
			Created: s.now().Unix(), Tags: tags,
		}}
		out = u
		return putJSON(tx, userBucket, name, u)
	})
	return out, err
}

func (s *Store) GetUser(name string) (*User, error) {
	var out *User
	err := s.db.View(func(tx *bolt.Tx) error {
		u, ok := getJSON[User](tx, userBucket, name)
		if !ok {
			return errNoEntity("The user with name %s cannot be found.", name)
		}
		out = u
		return nil
	})
	return out, err
}

func (s *Store) UpdateUser(name string, fn func(*User) error) (*User, error) {
	var out *User
	err := s.db.Update(func(tx *bolt.Tx) error {
		u, ok := getJSON[User](tx, userBucket, name)
		if !ok {
			return errNoEntity("The user with name %s cannot be found.", name)
		}
		if err := fn(u); err != nil {
			return err
		}
		out = u
		// A rename moves the record; the old key must go.
		if u.Name != name {
			if b := tx.Bucket(userBucket); b != nil {
				_ = b.Delete([]byte(name))
			}
		}
		return putJSON(tx, userBucket, u.Name, u)
	})
	return out, err
}

// DeleteUser refuses while the user still has attached policies or access
// keys, matching AWS's DeleteConflict rather than silently orphaning them.
func (s *Store) DeleteUser(name string) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		u, ok := getJSON[User](tx, userBucket, name)
		if !ok {
			return errNoEntity("The user with name %s cannot be found.", name)
		}
		if len(u.Attached) > 0 {
			return errDeleteConflict("Cannot delete entity, must detach all policies first.")
		}
		for _, k := range listJSON[AccessKey](tx, keyBucket) {
			if k.UserName == name {
				return errDeleteConflict("Cannot delete entity, must delete access keys first.")
			}
		}
		return tx.Bucket(userBucket).Delete([]byte(name))
	})
}

func (s *Store) ListUsers(pathPrefix string) ([]User, error) {
	var out []User
	err := s.db.View(func(tx *bolt.Tx) error {
		for _, u := range listJSON[User](tx, userBucket) {
			if pathPrefix == "" || strings.HasPrefix(u.Path, pathPrefix) {
				out = append(out, u)
			}
		}
		return nil
	})
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, err
}

// ---- groups ----

func (s *Store) CreateGroup(name, path string) (*Group, error) {
	if err := validName("GroupName", name); err != nil {
		return nil, err
	}
	var out *Group
	err := s.db.Update(func(tx *bolt.Tx) error {
		if _, ok := getJSON[Group](tx, groupBucket, name); ok {
			return errEntityExists("Group with name %s already exists.", name)
		}
		g := &Group{Principal{Name: name, Path: normPath(path), ID: uniqueID("AGPA"), Created: s.now().Unix()}}
		out = g
		return putJSON(tx, groupBucket, name, g)
	})
	return out, err
}

func (s *Store) GetGroup(name string) (*Group, error) {
	var out *Group
	err := s.db.View(func(tx *bolt.Tx) error {
		g, ok := getJSON[Group](tx, groupBucket, name)
		if !ok {
			return errNoEntity("The group with name %s cannot be found.", name)
		}
		out = g
		return nil
	})
	return out, err
}

func (s *Store) UpdateGroup(name string, fn func(*Group) error) (*Group, error) {
	var out *Group
	err := s.db.Update(func(tx *bolt.Tx) error {
		g, ok := getJSON[Group](tx, groupBucket, name)
		if !ok {
			return errNoEntity("The group with name %s cannot be found.", name)
		}
		if err := fn(g); err != nil {
			return err
		}
		out = g
		if g.Name != name {
			if b := tx.Bucket(groupBucket); b != nil {
				_ = b.Delete([]byte(name))
			}
		}
		return putJSON(tx, groupBucket, g.Name, g)
	})
	return out, err
}

func (s *Store) DeleteGroup(name string) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		g, ok := getJSON[Group](tx, groupBucket, name)
		if !ok {
			return errNoEntity("The group with name %s cannot be found.", name)
		}
		if len(g.Attached) > 0 {
			return errDeleteConflict("Cannot delete entity, must detach all policies first.")
		}
		return tx.Bucket(groupBucket).Delete([]byte(name))
	})
}

func (s *Store) ListGroups(pathPrefix string) ([]Group, error) {
	var out []Group
	err := s.db.View(func(tx *bolt.Tx) error {
		for _, g := range listJSON[Group](tx, groupBucket) {
			if pathPrefix == "" || strings.HasPrefix(g.Path, pathPrefix) {
				out = append(out, g)
			}
		}
		return nil
	})
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, err
}

// ---- roles ----

func (s *Store) CreateRole(name, path, assumeRolePolicy, description string, maxSession int, tags map[string]string) (*Role, error) {
	if err := validName("RoleName", name); err != nil {
		return nil, err
	}
	if assumeRolePolicy != "" {
		if _, err := ParsePolicy(assumeRolePolicy); err != nil {
			return nil, errMalformedPolicy("AssumeRolePolicyDocument: %v", err)
		}
	}
	var out *Role
	err := s.db.Update(func(tx *bolt.Tx) error {
		if _, ok := getJSON[Role](tx, roleBucket, name); ok {
			return errEntityExists("Role with name %s already exists.", name)
		}
		r := &Role{
			Principal:          Principal{Name: name, Path: normPath(path), ID: uniqueID("AROA"), Created: s.now().Unix(), Tags: tags},
			AssumeRolePolicy:   assumeRolePolicy,
			Description:        description,
			MaxSessionDuration: orDefault(maxSession, 3600),
		}
		out = r
		return putJSON(tx, roleBucket, name, r)
	})
	return out, err
}

func (s *Store) GetRole(name string) (*Role, error) {
	var out *Role
	err := s.db.View(func(tx *bolt.Tx) error {
		r, ok := getJSON[Role](tx, roleBucket, name)
		if !ok {
			return errNoEntity("The role with name %s cannot be found.", name)
		}
		out = r
		return nil
	})
	return out, err
}

func (s *Store) UpdateRole(name string, fn func(*Role) error) (*Role, error) {
	var out *Role
	err := s.db.Update(func(tx *bolt.Tx) error {
		r, ok := getJSON[Role](tx, roleBucket, name)
		if !ok {
			return errNoEntity("The role with name %s cannot be found.", name)
		}
		if err := fn(r); err != nil {
			return err
		}
		out = r
		return putJSON(tx, roleBucket, r.Name, r)
	})
	return out, err
}

func (s *Store) DeleteRole(name string) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		r, ok := getJSON[Role](tx, roleBucket, name)
		if !ok {
			return errNoEntity("The role with name %s cannot be found.", name)
		}
		if len(r.Attached) > 0 {
			return errDeleteConflict("Cannot delete entity, must detach all policies first.")
		}
		return tx.Bucket(roleBucket).Delete([]byte(name))
	})
}

func (s *Store) ListRoles(pathPrefix string) ([]Role, error) {
	var out []Role
	err := s.db.View(func(tx *bolt.Tx) error {
		for _, r := range listJSON[Role](tx, roleBucket) {
			if pathPrefix == "" || strings.HasPrefix(r.Path, pathPrefix) {
				out = append(out, r)
			}
		}
		return nil
	})
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, err
}

// ---- managed policies ----

func (s *Store) CreatePolicy(name, path, document, description string, tags map[string]string) (*Policy, error) {
	if err := validName("PolicyName", name); err != nil {
		return nil, err
	}
	if _, err := ParsePolicy(document); err != nil {
		return nil, errMalformedPolicy("PolicyDocument: %v", err)
	}
	var out *Policy
	err := s.db.Update(func(tx *bolt.Tx) error {
		key := normPath(path) + name
		if _, ok := getJSON[Policy](tx, policyBucket, key); ok {
			return errEntityExists("A policy called %s already exists.", name)
		}
		now := s.now().Unix()
		p := &Policy{
			Name: name, Path: normPath(path), ID: uniqueID("ANPA"),
			Created: now, Updated: now, Description: description,
			DefaultVersion: "v1",
			Versions:       map[string]string{"v1": document},
			VersionOrder:   []string{"v1"},
			Tags:           tags,
		}
		out = p
		return putJSON(tx, policyBucket, key, p)
	})
	return out, err
}

// policyKey turns a policy ARN back into its store key (path + name).
func policyKey(arn string) string {
	i := strings.Index(arn, ":policy")
	if i < 0 {
		return ""
	}
	return arn[i+len(":policy"):]
}

// GetPolicy resolves a policy ARN. AWS-managed ARNs are synthesized rather
// than stored — see managed.go.
func (s *Store) GetPolicy(arn string) (*Policy, error) {
	if p, ok := managedPolicy(arn); ok {
		return p, nil
	}
	key := policyKey(arn)
	if key == "" {
		return nil, errNoEntity("Policy %s does not exist or is not attachable.", arn)
	}
	var out *Policy
	err := s.db.View(func(tx *bolt.Tx) error {
		p, ok := getJSON[Policy](tx, policyBucket, key)
		if !ok {
			return errNoEntity("Policy %s does not exist or is not attachable.", arn)
		}
		out = p
		return nil
	})
	return out, err
}

func (s *Store) UpdatePolicy(arn string, fn func(*Policy) error) (*Policy, error) {
	if isManagedARN(arn) {
		return nil, errInvalidInput("Policy %s is an AWS managed policy and cannot be modified.", arn)
	}
	key := policyKey(arn)
	var out *Policy
	err := s.db.Update(func(tx *bolt.Tx) error {
		p, ok := getJSON[Policy](tx, policyBucket, key)
		if !ok {
			return errNoEntity("Policy %s does not exist or is not attachable.", arn)
		}
		if err := fn(p); err != nil {
			return err
		}
		p.Updated = s.now().Unix()
		out = p
		return putJSON(tx, policyBucket, key, p)
	})
	return out, err
}

func (s *Store) DeletePolicy(arn string) error {
	if isManagedARN(arn) {
		return errInvalidInput("Policy %s is an AWS managed policy and cannot be deleted.", arn)
	}
	key := policyKey(arn)
	return s.db.Update(func(tx *bolt.Tx) error {
		p, ok := getJSON[Policy](tx, policyBucket, key)
		if !ok {
			return errNoEntity("Policy %s does not exist or is not attachable.", arn)
		}
		if p.AttachCount > 0 {
			return errDeleteConflict("Cannot delete a policy attached to entities.")
		}
		return tx.Bucket(policyBucket).Delete([]byte(key))
	})
}

func (s *Store) ListPolicies(pathPrefix string) ([]Policy, error) {
	var out []Policy
	err := s.db.View(func(tx *bolt.Tx) error {
		for _, p := range listJSON[Policy](tx, policyBucket) {
			if pathPrefix == "" || strings.HasPrefix(p.Path, pathPrefix) {
				out = append(out, p)
			}
		}
		return nil
	})
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, err
}

// AddPolicyVersion appends a version, enforcing the five-version ceiling.
func (s *Store) AddPolicyVersion(arn, document string, setDefault bool) (string, error) {
	if _, err := ParsePolicy(document); err != nil {
		return "", errMalformedPolicy("PolicyDocument: %v", err)
	}
	version := ""
	_, err := s.UpdatePolicy(arn, func(p *Policy) error {
		if len(p.VersionOrder) >= maxPolicyVersions {
			return errLimitExceeded("A managed policy can have up to %d versions. Delete an existing version first.", maxPolicyVersions)
		}
		version = fmt.Sprintf("v%d", nextVersionNumber(p.VersionOrder))
		p.Versions[version] = document
		p.VersionOrder = append(p.VersionOrder, version)
		if setDefault {
			p.DefaultVersion = version
		}
		return nil
	})
	return version, err
}

// nextVersionNumber returns one past the highest vN seen, so deleting v2 and
// adding again yields v4 rather than reusing v2 — which is what AWS does.
func nextVersionNumber(order []string) int {
	high := 0
	for _, v := range order {
		var n int
		if _, err := fmt.Sscanf(v, "v%d", &n); err == nil && n > high {
			high = n
		}
	}
	return high + 1
}

func (s *Store) DeletePolicyVersion(arn, version string) error {
	_, err := s.UpdatePolicy(arn, func(p *Policy) error {
		if version == p.DefaultVersion {
			return errDeleteConflict("Cannot delete the default version of a policy.")
		}
		if _, ok := p.Versions[version]; !ok {
			return errNoEntity("Policy %s version %s does not exist.", arn, version)
		}
		delete(p.Versions, version)
		p.VersionOrder = removeString(p.VersionOrder, version)
		return nil
	})
	return err
}

// ---- attachment ----

// attachTarget names the principal kind an attach/detach applies to.
type attachTarget int

const (
	targetUser attachTarget = iota
	targetGroup
	targetRole
)

// Attach adds a managed policy ARN to a principal. It is idempotent, and it
// keeps the policy's attachment count in step so DeletePolicy can refuse.
func (s *Store) Attach(kind attachTarget, name, policyARN string) error {
	if _, err := s.GetPolicy(policyARN); err != nil {
		return err
	}
	added := false
	if err := s.updatePrincipal(kind, name, func(p *Principal) error {
		if containsString(p.Attached, policyARN) {
			return nil
		}
		p.Attached = append(p.Attached, policyARN)
		added = true
		return nil
	}); err != nil {
		return err
	}
	if added && !isManagedARN(policyARN) {
		_, _ = s.UpdatePolicy(policyARN, func(p *Policy) error { p.AttachCount++; return nil })
	}
	return nil
}

func (s *Store) Detach(kind attachTarget, name, policyARN string) error {
	removed := false
	if err := s.updatePrincipal(kind, name, func(p *Principal) error {
		if !containsString(p.Attached, policyARN) {
			return errNoEntity("Policy %s is not attached to entity %s.", policyARN, name)
		}
		p.Attached = removeString(p.Attached, policyARN)
		removed = true
		return nil
	}); err != nil {
		return err
	}
	if removed && !isManagedARN(policyARN) {
		_, _ = s.UpdatePolicy(policyARN, func(p *Policy) error {
			if p.AttachCount > 0 {
				p.AttachCount--
			}
			return nil
		})
	}
	return nil
}

// updatePrincipal applies fn to the embedded Principal of any principal kind,
// which is what lets attach/detach and inline-policy handling be written once.
func (s *Store) updatePrincipal(kind attachTarget, name string, fn func(*Principal) error) error {
	var err error
	switch kind {
	case targetUser:
		_, err = s.UpdateUser(name, func(u *User) error { return fn(&u.Principal) })
	case targetGroup:
		_, err = s.UpdateGroup(name, func(g *Group) error { return fn(&g.Principal) })
	case targetRole:
		_, err = s.UpdateRole(name, func(r *Role) error { return fn(&r.Principal) })
	}
	return err
}

// principalOf fetches the embedded Principal for a kind and name.
func (s *Store) principalOf(kind attachTarget, name string) (*Principal, error) {
	switch kind {
	case targetUser:
		u, err := s.GetUser(name)
		if err != nil {
			return nil, err
		}
		return &u.Principal, nil
	case targetGroup:
		g, err := s.GetGroup(name)
		if err != nil {
			return nil, err
		}
		return &g.Principal, nil
	default:
		r, err := s.GetRole(name)
		if err != nil {
			return nil, err
		}
		return &r.Principal, nil
	}
}

// ---- access keys ----

func (s *Store) CreateAccessKey(user string) (*AccessKey, error) {
	if _, err := s.GetUser(user); err != nil {
		return nil, err
	}
	k := &AccessKey{
		ID:       uniqueID("AKIA"),
		Secret:   randomSecret(),
		UserName: user,
		Status:   "Active",
		Created:  s.now().Unix(),
	}
	err := s.db.Update(func(tx *bolt.Tx) error { return putJSON(tx, keyBucket, k.ID, k) })
	return k, err
}

// LookupAccessKey resolves a key id to its key. This is the hot path for
// enforcement: one point lookup, no scan.
func (s *Store) LookupAccessKey(id string) (*AccessKey, bool) {
	var out *AccessKey
	_ = s.db.View(func(tx *bolt.Tx) error {
		k, ok := getJSON[AccessKey](tx, keyBucket, id)
		if ok {
			out = k
		}
		return nil
	})
	return out, out != nil
}

func (s *Store) ListAccessKeys(user string) ([]AccessKey, error) {
	var out []AccessKey
	err := s.db.View(func(tx *bolt.Tx) error {
		for _, k := range listJSON[AccessKey](tx, keyBucket) {
			if k.UserName == user {
				out = append(out, k)
			}
		}
		return nil
	})
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, err
}

func (s *Store) UpdateAccessKey(id, status string) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		k, ok := getJSON[AccessKey](tx, keyBucket, id)
		if !ok {
			return errNoEntity("The Access Key with id %s cannot be found.", id)
		}
		k.Status = status
		return putJSON(tx, keyBucket, id, k)
	})
}

func (s *Store) DeleteAccessKey(id string) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		if _, ok := getJSON[AccessKey](tx, keyBucket, id); !ok {
			return errNoEntity("The Access Key with id %s cannot be found.", id)
		}
		return tx.Bucket(keyBucket).Delete([]byte(id))
	})
}

// TouchAccessKey records last-used metadata, which GetAccessKeyLastUsed reads.
func (s *Store) TouchAccessKey(id, service string) {
	_ = s.db.Update(func(tx *bolt.Tx) error {
		k, ok := getJSON[AccessKey](tx, keyBucket, id)
		if !ok {
			return nil
		}
		k.LastUsed, k.LastSvc = s.now().Unix(), service
		return putJSON(tx, keyBucket, id, k)
	})
}

// ---- instance profiles ----

func (s *Store) CreateInstanceProfile(name, path string) (*InstanceProfile, error) {
	if err := validName("InstanceProfileName", name); err != nil {
		return nil, err
	}
	var out *InstanceProfile
	err := s.db.Update(func(tx *bolt.Tx) error {
		if _, ok := getJSON[InstanceProfile](tx, profileBucket, name); ok {
			return errEntityExists("Instance Profile %s already exists.", name)
		}
		p := &InstanceProfile{Name: name, Path: normPath(path), ID: uniqueID("AIPA"), Created: s.now().Unix()}
		out = p
		return putJSON(tx, profileBucket, name, p)
	})
	return out, err
}

func (s *Store) GetInstanceProfile(name string) (*InstanceProfile, error) {
	var out *InstanceProfile
	err := s.db.View(func(tx *bolt.Tx) error {
		p, ok := getJSON[InstanceProfile](tx, profileBucket, name)
		if !ok {
			return errNoEntity("Instance Profile %s cannot be found.", name)
		}
		out = p
		return nil
	})
	return out, err
}

func (s *Store) UpdateInstanceProfile(name string, fn func(*InstanceProfile) error) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		p, ok := getJSON[InstanceProfile](tx, profileBucket, name)
		if !ok {
			return errNoEntity("Instance Profile %s cannot be found.", name)
		}
		if err := fn(p); err != nil {
			return err
		}
		return putJSON(tx, profileBucket, name, p)
	})
}

func (s *Store) DeleteInstanceProfile(name string) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		if _, ok := getJSON[InstanceProfile](tx, profileBucket, name); !ok {
			return errNoEntity("Instance Profile %s cannot be found.", name)
		}
		return tx.Bucket(profileBucket).Delete([]byte(name))
	})
}

func (s *Store) ListInstanceProfiles() ([]InstanceProfile, error) {
	var out []InstanceProfile
	err := s.db.View(func(tx *bolt.Tx) error {
		out = listJSON[InstanceProfile](tx, profileBucket)
		return nil
	})
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, err
}

// ---- account ----

func (s *Store) AccountAlias() string {
	alias := ""
	_ = s.db.View(func(tx *bolt.Tx) error {
		if b := tx.Bucket(accountBucket); b != nil {
			alias = string(b.Get([]byte("alias")))
		}
		return nil
	})
	return alias
}

func (s *Store) SetAccountAlias(alias string) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		b, err := tx.CreateBucketIfNotExists(accountBucket)
		if err != nil {
			return err
		}
		if alias == "" {
			return b.Delete([]byte("alias"))
		}
		return b.Put([]byte("alias"), []byte(alias))
	})
}

// ---- policy resolution for evaluation ----

// PoliciesFor gathers every policy document that applies to a principal:
// inline, attached managed, and — for users — everything inherited from their
// groups. This is what Evaluate is handed.
func (s *Store) PoliciesFor(kind attachTarget, name string) ([]*Document, error) {
	p, err := s.principalOf(kind, name)
	if err != nil {
		return nil, err
	}
	docs := s.documentsOf(p)

	if kind == targetUser {
		u, err := s.GetUser(name)
		if err == nil {
			for _, gname := range u.Groups {
				if g, err := s.GetGroup(gname); err == nil {
					docs = append(docs, s.documentsOf(&g.Principal)...)
				}
			}
		}
	}
	return docs, nil
}

// documentsOf parses a principal's inline and attached policies. Unparseable
// documents are skipped rather than failing the whole evaluation — they could
// only have been stored before validation tightened.
func (s *Store) documentsOf(p *Principal) []*Document {
	var docs []*Document
	names := make([]string, 0, len(p.Inline))
	for n := range p.Inline {
		names = append(names, n)
	}
	sort.Strings(names) // deterministic attribution in Evaluate
	for _, n := range names {
		if d, err := ParsePolicy(p.Inline[n]); err == nil {
			if d.ID == "" {
				d.ID = n
			}
			docs = append(docs, d)
		}
	}
	for _, arn := range p.Attached {
		pol, err := s.GetPolicy(arn)
		if err != nil {
			continue
		}
		if d, err := ParsePolicy(pol.Default()); err == nil {
			if d.ID == "" {
				d.ID = pol.Name
			}
			docs = append(docs, d)
		}
	}
	return docs
}

// ---- small helpers ----

func containsString(list []string, v string) bool {
	for _, item := range list {
		if item == v {
			return true
		}
	}
	return false
}

func removeString(list []string, v string) []string {
	out := list[:0]
	for _, item := range list {
		if item != v {
			out = append(out, item)
		}
	}
	return out
}

func orDefault(v, def int) int {
	if v == 0 {
		return def
	}
	return v
}

// validName enforces the IAM name charset so a name that works locally works
// in the cloud.
func validName(field, name string) error {
	if name == "" {
		return errValidation("%s is required", field)
	}
	if len(name) > 128 {
		return errValidation("%s may be at most 128 characters", field)
	}
	for _, r := range name {
		ok := r == '_' || r == '+' || r == '=' || r == ',' || r == '.' || r == '@' || r == '-' ||
			(r >= '0' && r <= '9') || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')
		if !ok {
			return errValidation("%s may contain only alphanumerics and _+=,.@-", field)
		}
	}
	return nil
}

func randomSecret() string {
	const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"
	b := make([]byte, 40)
	_, _ = rand.Read(b)
	for i := range b {
		b[i] = alphabet[int(b[i])%len(alphabet)]
	}
	return string(b)
}
