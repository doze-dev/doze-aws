package cloudwatch

// Dashboards: a JSON document stored under a name.
//
// Tier C, and deliberately so. AWS renders the body into widgets; nothing
// here does, and pretending otherwise would be the worse failure. What a
// developer actually needs locally is that a template declaring a dashboard
// deploys, that the body round-trips byte for byte, and that CDK's
// `Dashboard` construct stops being the reason a stack will not transpile.
//
// The body is stored verbatim rather than re-marshalled. A dashboard body is
// authored JSON — widget positions, titles, metric expressions — and a
// round-trip through map[string]any would reorder its keys and reformat its
// numbers, so a caller diffing what it wrote against what it reads back would
// see changes it did not make.

import (
	"encoding/json"
	"strings"

	bolt "go.etcd.io/bbolt"

	"github.com/doze-dev/doze-aws/internal/awshttp"
)

var bucketDashboards = []byte("dashboards")

type dashboard struct {
	Name      string            `json:"name"`
	Body      string            `json:"body"`
	Tags      map[string]string `json:"tags,omitempty"`
	UpdatedMs int64             `json:"updated_ms"`
}

// dashboardARN is what a tag operation names and what GetDashboard reports.
func (s *Server) dashboardARN(name string) string {
	return s.id.ARN("cloudwatch", "dashboard/"+name)
}

func (s *Server) putDashboard(d dashboard) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		b, err := tx.CreateBucketIfNotExists(bucketDashboards)
		if err != nil {
			return err
		}
		// A put replaces the body but keeps tags a TagResource added, which
		// is AWS's behaviour: tags belong to the resource, not to the body.
		if prev := b.Get([]byte(d.Name)); prev != nil && len(d.Tags) == 0 {
			var old dashboard
			if json.Unmarshal(prev, &old) == nil {
				d.Tags = old.Tags
			}
		}
		raw, err := json.Marshal(d)
		if err != nil {
			return err
		}
		return b.Put([]byte(d.Name), raw)
	})
}

// readDashboard answers ErrNoDashboard when nothing is stored under the name.
func (s *Server) readDashboard(name string) (dashboard, error) {
	var d dashboard
	err := s.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(bucketDashboards)
		if b == nil {
			return errNoDashboard(name)
		}
		raw := b.Get([]byte(name))
		if raw == nil {
			return errNoDashboard(name)
		}
		return json.Unmarshal(raw, &d)
	})
	return d, err
}

func (s *Server) listDashboards(prefix string) ([]dashboard, error) {
	var out []dashboard
	err := s.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(bucketDashboards)
		if b == nil {
			return nil
		}
		return b.ForEach(func(k, v []byte) error {
			if prefix != "" && !strings.HasPrefix(string(k), prefix) {
				return nil
			}
			var d dashboard
			if json.Unmarshal(v, &d) != nil {
				return nil
			}
			out = append(out, d)
			return nil
		})
	})
	return out, err
}

// deleteDashboards removes them all or none: AWS's DeleteDashboards is
// atomic, answering ResourceNotFound without deleting anything when one of
// the names does not exist.
func (s *Server) deleteDashboards(names []string) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(bucketDashboards)
		if b == nil {
			return errNoDashboard(names[0])
		}
		for _, n := range names {
			if b.Get([]byte(n)) == nil {
				return errNoDashboard(n)
			}
		}
		for _, n := range names {
			if err := b.Delete([]byte(n)); err != nil {
				return err
			}
		}
		return nil
	})
}

func errNoDashboard(name string) *awshttp.APIError {
	return errf("ResourceNotFound", "Dashboard %s does not exist.", name)
}
