package console

// The secret's resource policy: read, written and deleted from the secret
// page. Under IAM soft or enforce Secrets Manager evaluates it on every
// request naming the secret, so this is the panel that lets an identity
// with no policy of its own read a secret — or keeps a granted one out.

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
)

// SecretPolicy reads the resource policy (GetResourcePolicy); "" when the
// secret has none.
func (b *backend) SecretPolicy(ctx context.Context, id string) (string, error) {
	body, err := b.json11(ctx, "secretsmanager", "GetResourcePolicy", map[string]any{"SecretId": id})
	if err != nil {
		return "", err
	}
	var out struct {
		ResourcePolicy string `json:"ResourcePolicy"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return "", err
	}
	if out.ResourcePolicy == "" {
		return "", nil
	}
	return prettyJSON(out.ResourcePolicy), nil
}

// PutSecretPolicy replaces the resource policy (PutResourcePolicy); an empty
// document deletes it (DeleteResourcePolicy), which is how the panel's
// "Remove" works.
func (b *backend) PutSecretPolicy(ctx context.Context, id, doc string) error {
	if strings.TrimSpace(doc) == "" {
		_, err := b.json11(ctx, "secretsmanager", "DeleteResourcePolicy", map[string]any{"SecretId": id})
		return err
	}
	_, err := b.json11(ctx, "secretsmanager", "PutResourcePolicy", map[string]any{"SecretId": id, "ResourcePolicy": doc})
	return err
}

// smSavePolicy writes the policy from the builder and re-renders the detail.
func (c *Console) smSavePolicy(w http.ResponseWriter, r *http.Request) {
	name := r.FormValue("name")
	doc := strings.TrimSpace(r.FormValue("document"))
	if r.FormValue("remove") == "1" {
		doc = ""
	}
	if err := c.be.PutSecretPolicy(r.Context(), name, doc); err != nil {
		c.fail(w, err)
		return
	}
	if doc == "" {
		toast(w, "Resource policy removed")
	} else {
		toast(w, "Resource policy saved — evaluated under IAM soft and enforce")
	}
	c.smRotationRefresh(w, r, name)
}
