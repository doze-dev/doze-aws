package console

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// urlQuery escapes a value for a query-string component.
func urlQuery(v string) string { return url.QueryEscape(v) }

// ---- KMS ----

func (c *Console) kmsKeys(w http.ResponseWriter, r *http.Request) {
	keys, err := c.be.ListKeys(r.Context())
	if err != nil {
		c.fail(w, err)
		return
	}
	if len(keys) > 0 {
		setParam(r, "key", keys[0].ID)
		c.kmsKey(w, r)
		return
	}
	c.render(w, r, "kms_home", map[string]any{"List": keys, "Title": "KMS"})
}

func (c *Console) kmsCreateKey(w http.ResponseWriter, r *http.Request) {
	id, err := c.be.CreateKey(r.Context(),
		r.FormValue("spec"), r.FormValue("usage"),
		r.FormValue("alias"), r.FormValue("description"))
	if err != nil {
		c.fail(w, err)
		return
	}
	c.redirect(w, r, c.prefix+"/kms/"+id, "Key created")
}

func (c *Console) kmsKey(w http.ResponseWriter, r *http.Request) {
	key, err := c.be.DescribeKey(r.Context(), param(r, "key"))
	if err != nil {
		c.fail(w, err)
		return
	}
	keys, _ := c.be.ListKeys(r.Context())
	pol, _ := c.be.KeyPolicy(r.Context(), key.ID)
	c.render(w, r, "kms_key", map[string]any{"Key": key, "List": keys, "Title": "KMS",
		"KeyPolicy": pol, "SeenActions": c.seenActions(r)})
}

func (c *Console) kmsKeyPartial(w http.ResponseWriter, r *http.Request) {
	key, err := c.be.DescribeKey(r.Context(), param(r, "key"))
	if err != nil {
		c.fail(w, err)
		return
	}
	pol, _ := c.be.KeyPolicy(r.Context(), key.ID)
	c.partial(w, "kms_key_detail", map[string]any{"Key": key, "KeyPolicy": pol})
}

func (c *Console) kmsToggleEnabled(w http.ResponseWriter, r *http.Request) {
	id := param(r, "key")
	key, err := c.be.DescribeKey(r.Context(), id)
	if err != nil {
		c.fail(w, err)
		return
	}
	if err := c.be.SetKeyEnabled(r.Context(), id, !key.Enabled); err != nil {
		c.fail(w, err)
		return
	}
	if key.Enabled {
		toast(w, "Key disabled")
	} else {
		toast(w, "Key enabled")
	}
	c.kmsKeyPartial(w, r)
}

func (c *Console) kmsToggleRotation(w http.ResponseWriter, r *http.Request) {
	id := param(r, "key")
	key, err := c.be.DescribeKey(r.Context(), id)
	if err != nil {
		c.fail(w, err)
		return
	}
	if err := c.be.SetKeyRotation(r.Context(), id, !key.RotationOn); err != nil {
		c.fail(w, err)
		return
	}
	if key.RotationOn {
		toast(w, "Automatic rotation disabled")
	} else {
		toast(w, "Automatic rotation enabled")
	}
	c.kmsKeyPartial(w, r)
}

func (c *Console) kmsRotateNow(w http.ResponseWriter, r *http.Request) {
	if err := c.be.RotateKeyNow(r.Context(), param(r, "key")); err != nil {
		c.fail(w, err)
		return
	}
	toast(w, "Key material rotated")
	c.kmsKeyPartial(w, r)
}

func (c *Console) kmsScheduleDeletion(w http.ResponseWriter, r *http.Request) {
	if err := c.be.ScheduleKeyDeletion(r.Context(), param(r, "key")); err != nil {
		c.fail(w, err)
		return
	}
	toast(w, "Deletion scheduled (7 days)")
	w.Header().Set("HX-Redirect", c.prefix+"/kms")
}

func (c *Console) kmsEncrypt(w http.ResponseWriter, r *http.Request) {
	out, err := c.be.KMSEncrypt(r.Context(), param(r, "key"), r.FormValue("plaintext"))
	if err != nil {
		c.fail(w, err)
		return
	}
	c.partial(w, "kms_crypto_result", map[string]any{
		"Label": "Ciphertext (base64)", "Value": out,
		// One-click round-trip: the result card can decrypt itself.
		"DecryptURL": c.prefix + "/kms/" + param(r, "key") + "/decrypt",
	})
}

func (c *Console) kmsDecrypt(w http.ResponseWriter, r *http.Request) {
	out, err := c.be.KMSDecrypt(r.Context(), r.FormValue("ciphertext"))
	if err != nil {
		c.fail(w, err)
		return
	}
	c.partial(w, "kms_crypto_result", map[string]any{"Label": "Plaintext", "Value": out})
}

// kmsSign / kmsVerify — asymmetric signing playground.
func (c *Console) kmsSign(w http.ResponseWriter, r *http.Request) {
	key := param(r, "key")
	sig, err := c.be.KMSSign(r.Context(), key, r.FormValue("algo"), r.FormValue("message"))
	if err != nil {
		c.fail(w, err)
		return
	}
	c.partial(w, "kms_sign_result", map[string]any{
		"Prefix": c.prefix, "Key": key, "Algo": r.FormValue("algo"),
		"Message": r.FormValue("message"), "Signature": sig,
	})
}

func (c *Console) kmsVerify(w http.ResponseWriter, r *http.Request) {
	err := c.be.KMSVerify(r.Context(), param(r, "key"), r.FormValue("algo"), r.FormValue("message"), r.FormValue("signature"))
	c.partial(w, "kms_verdict", map[string]any{"Valid": err == nil, "What": "signature"})
}

// kmsMac / kmsVerifyMac — HMAC playground.
func (c *Console) kmsMac(w http.ResponseWriter, r *http.Request) {
	key := param(r, "key")
	mac, err := c.be.KMSGenerateMac(r.Context(), key, r.FormValue("algo"), r.FormValue("message"))
	if err != nil {
		c.fail(w, err)
		return
	}
	c.partial(w, "kms_mac_result", map[string]any{
		"Prefix": c.prefix, "Key": key, "Algo": r.FormValue("algo"),
		"Message": r.FormValue("message"), "Mac": mac,
	})
}

func (c *Console) kmsVerifyMac(w http.ResponseWriter, r *http.Request) {
	err := c.be.KMSVerifyMac(r.Context(), param(r, "key"), r.FormValue("algo"), r.FormValue("message"), r.FormValue("mac"))
	c.partial(w, "kms_verdict", map[string]any{"Valid": err == nil, "What": "MAC"})
}

func (c *Console) kmsAddAlias(w http.ResponseWriter, r *http.Request) {
	key := param(r, "key")
	if err := c.be.KMSAddAlias(r.Context(), key, r.FormValue("alias")); err != nil {
		c.fail(w, err)
		return
	}
	toast(w, "Alias added")
	c.kmsKeyPartial(w, r)
}

func (c *Console) kmsDeleteAlias(w http.ResponseWriter, r *http.Request) {
	if err := c.be.KMSDeleteAlias(r.Context(), r.FormValue("alias")); err != nil {
		c.fail(w, err)
		return
	}
	toast(w, "Alias removed")
	c.kmsKeyPartial(w, r)
}

func (c *Console) kmsCancelDeletion(w http.ResponseWriter, r *http.Request) {
	if err := c.be.KMSCancelDeletion(r.Context(), param(r, "key")); err != nil {
		c.fail(w, err)
		return
	}
	toast(w, "Deletion cancelled — the key is usable again")
	c.kmsKeyPartial(w, r)
}

// ---- SSM Parameter Store ----

func (c *Console) ssmParams(w http.ResponseWriter, r *http.Request) {
	params, err := c.be.ListParameters(r.Context())
	if err != nil {
		c.fail(w, err)
		return
	}
	c.render(w, r, "ssm_home", map[string]any{"List": params, "Title": "Parameter Store"})
}

func (c *Console) ssmCreate(w http.ResponseWriter, r *http.Request) {
	name := r.FormValue("name")
	if err := c.be.PutParameter(r.Context(), name, r.FormValue("value"), r.FormValue("type"), false); err != nil {
		c.fail(w, err)
		return
	}
	c.redirect(w, r, c.prefix+"/ssm/param?name="+urlQuery(name), "Parameter “"+name+"” created")
}

func (c *Console) ssmParam(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("name")
	p, err := c.be.GetParameter(r.Context(), name)
	if err != nil {
		c.fail(w, err)
		return
	}
	v := c.paramDetailView(r.Context(), p, tabOf(r, "view"))
	all, _ := c.be.ListParameters(r.Context())
	v["List"], v["Sel"] = all, name
	v["Title"] = p.Name + " · Parameter Store"
	c.render(w, r, "ssm_param", v)
}

// paramDetailView is what ssm_param_detail renders from, for the page and for
// every fragment that redraws it. A fragment that passed only the parameter and
// its history drew a SecureString in clear with its badge gone, because
// "Secure" was one of the things it left out.
func (c *Console) paramDetailView(ctx context.Context, p *Parameter, mode string) map[string]any {
	hist, _ := c.be.ParameterHistory(ctx, p.Name)
	return map[string]any{"P": p, "History": hist, "Mode": mode, "Secure": p.Type == "SecureString", "Prefix": c.prefix}
}

// ssmDetailPartial redraws the parameter's detail after an action.
func (c *Console) ssmDetailPartial(w http.ResponseWriter, r *http.Request, name, mode string) {
	p, err := c.be.GetParameter(r.Context(), name)
	if err != nil {
		c.fail(w, err)
		return
	}
	c.partial(w, "ssm_param_detail", c.paramDetailView(r.Context(), p, mode))
}

func (c *Console) ssmPut(w http.ResponseWriter, r *http.Request) {
	name := r.FormValue("name")
	p, err := c.be.GetParameter(r.Context(), name)
	if err != nil {
		c.fail(w, err)
		return
	}
	if err := c.be.PutParameter(r.Context(), name, r.FormValue("value"), p.Type, true); err != nil {
		c.fail(w, err)
		return
	}
	toast(w, "New version saved")
	c.ssmDetailPartial(w, r, name, "view")
}

// ssmLabel attaches a label to the parameter version the row names (0 = latest).
func (c *Console) ssmLabel(w http.ResponseWriter, r *http.Request) {
	name := r.FormValue("name")
	version, _ := strconv.Atoi(r.FormValue("version"))
	if err := c.be.LabelParameter(r.Context(), name, r.FormValue("label"), version); err != nil {
		c.fail(w, err)
		return
	}
	toast(w, "Label “"+r.FormValue("label")+"” applied")
	// Keep the user on the Versions tab where they submitted the label.
	c.ssmDetailPartial(w, r, name, "versions")
}

// ssmUnlabel detaches one label from one version — the other half of
// ssmLabel, which existed alone, making every label permanent.
func (c *Console) ssmUnlabel(w http.ResponseWriter, r *http.Request) {
	name := r.FormValue("name")
	version, _ := strconv.Atoi(r.FormValue("version"))
	if err := c.be.UnlabelParameter(r.Context(), name, r.FormValue("label"), version); err != nil {
		c.fail(w, err)
		return
	}
	toast(w, "Label “"+r.FormValue("label")+"” removed")
	c.ssmDetailPartial(w, r, name, "versions")
}

func (c *Console) ssmDelete(w http.ResponseWriter, r *http.Request) {
	if err := c.be.DeleteParameter(r.Context(), r.FormValue("name")); err != nil {
		c.fail(w, err)
		return
	}
	toast(w, "Parameter deleted")
	w.Header().Set("HX-Redirect", c.prefix+"/ssm")
}

// ---- Secrets Manager ----

func (c *Console) smSecrets(w http.ResponseWriter, r *http.Request) {
	secrets, err := c.be.ListSecrets(r.Context())
	if err != nil {
		c.fail(w, err)
		return
	}
	c.render(w, r, "sm_home", map[string]any{"List": secrets, "Title": "Secrets Manager"})
}

func (c *Console) smCreate(w http.ResponseWriter, r *http.Request) {
	name := r.FormValue("name")
	if err := c.be.CreateSecret(r.Context(), name, r.FormValue("value"), r.FormValue("description")); err != nil {
		c.fail(w, err)
		return
	}
	c.redirect(w, r, c.prefix+"/sm/secret?name="+urlQuery(name), "Secret “"+name+"” created")
}

// smRestore cancels a pending deletion inside the recovery window.
func (c *Console) smRestore(w http.ResponseWriter, r *http.Request) {
	name := r.FormValue("name")
	if err := c.be.RestoreSecret(r.Context(), name); err != nil {
		c.fail(w, err)
		return
	}
	c.redirect(w, r, c.prefix+"/sm/secret?name="+url.QueryEscape(name), "Secret “"+name+"” restored")
}

// secretDetailView is what sm_secret_detail renders from. The page and every
// fragment that redraws it call this, and only this: a fragment that built its
// own subset of the data drew a secret with its version count wrong and its
// resource policy reading "Not set", because the subset left them out.
func (c *Console) secretDetailView(ctx context.Context, name, mode string) (map[string]any, error) {
	s, err := c.be.GetSecret(ctx, name)
	if err != nil {
		return nil, err
	}
	// The full version list, deprecated ones included. DescribeSecret only
	// reports versions that still hold a stage label, so a secret rotated
	// twenty times showed a history two entries deep — the deprecated versions
	// ARE the history.
	if stages, err := c.be.SecretVersionIDs(ctx, name); err == nil && len(stages) > 0 {
		s.Stages = stages
	}
	fns, _ := c.be.ListFunctions(ctx)
	policy, _ := c.be.SecretPolicy(ctx, name)
	return map[string]any{"S": s, "Functions": fns, "Policy": policy, "Mode": mode, "Prefix": c.prefix}, nil
}

// smDetailPartial redraws the secret's detail after an action, from the same
// view the page is built from.
func (c *Console) smDetailPartial(w http.ResponseWriter, r *http.Request, name, mode string) {
	v, err := c.secretDetailView(r.Context(), name, mode)
	if err != nil {
		c.fail(w, err)
		return
	}
	c.partial(w, "sm_secret_detail", v)
}

func (c *Console) smSecret(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("name")
	v, err := c.secretDetailView(r.Context(), name, tabOf(r, "view"))
	if err != nil {
		c.fail(w, err)
		return
	}
	all, _ := c.be.ListSecrets(r.Context())
	v["List"], v["Sel"] = all, name
	v["Title"] = name + " · Secrets Manager"
	c.render(w, r, "sm_secret", v)
}

// smRotationPartial re-renders the secret detail (rotation strip lives there).
func (c *Console) smRotationRefresh(w http.ResponseWriter, r *http.Request, name string) {
	c.smDetailPartial(w, r, name, "view")
}

// smConfigureRotation sets or clears the rotation lambda + schedule.
func (c *Console) smConfigureRotation(w http.ResponseWriter, r *http.Request) {
	name := r.FormValue("name")
	if err := c.be.ConfigureRotation(r.Context(), name, r.FormValue("lambda"), atoi(r.FormValue("days"))); err != nil {
		c.fail(w, err)
		return
	}
	if r.FormValue("lambda") == "" {
		toast(w, "Rotation disabled")
	} else {
		toast(w, "Rotation configured")
	}
	c.smRotationRefresh(w, r, name)
}

// smRotateNow triggers an immediate rotation.
func (c *Console) smRotateNow(w http.ResponseWriter, r *http.Request) {
	name := r.FormValue("name")
	if err := c.be.RotateNow(r.Context(), name); err != nil {
		c.fail(w, err)
		return
	}
	toast(w, "Rotation triggered")
	c.smRotationRefresh(w, r, name)
}

// smPassword returns a generated password for the create/new-version forms.
func (c *Console) smPassword(w http.ResponseWriter, r *http.Request) {
	pw, err := c.be.GetRandomPassword(r.Context(), 24)
	if err != nil {
		c.fail(w, err)
		return
	}
	w.Header().Set("Content-Type", "text/plain")
	w.Write([]byte(pw))
}

func (c *Console) smPut(w http.ResponseWriter, r *http.Request) {
	name := r.FormValue("name")
	if err := c.be.PutSecretValue(r.Context(), name, r.FormValue("value")); err != nil {
		c.fail(w, err)
		return
	}
	toast(w, "New secret version stored")
	c.smDetailPartial(w, r, name, "view")
}

func (c *Console) smDelete(w http.ResponseWriter, r *http.Request) {
	force := r.FormValue("force") == "true"
	if err := c.be.DeleteSecret(r.Context(), r.FormValue("name"), force); err != nil {
		c.fail(w, err)
		return
	}
	if force {
		toast(w, "Secret deleted permanently")
	} else {
		toast(w, "Secret scheduled for deletion (7-day recovery)")
	}
	w.Header().Set("HX-Redirect", c.prefix+"/sm")
}

// ssmDiff renders a line diff of a historical parameter version vs current.
func (c *Console) ssmDiff(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("name")
	cur, err := c.be.GetParameter(r.Context(), name)
	if err != nil {
		c.fail(w, err)
		return
	}
	hist, _ := c.be.ParameterHistory(r.Context(), name)
	old := ""
	want := r.URL.Query().Get("v")
	for _, h := range hist {
		if strconv.FormatInt(h.Version, 10) == want {
			old = h.Value
		}
	}
	c.partial(w, "value_diff", map[string]any{
		"Diff": lineDiff(old, cur.Value), "OldLabel": "v" + want, "NewLabel": "current",
	})
}

// smDiff renders a line diff of a secret version vs current.
func (c *Console) smDiff(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("name")
	cur, err := c.be.GetSecret(r.Context(), name)
	if err != nil {
		c.fail(w, err)
		return
	}
	old, _ := c.be.GetSecretVersion(r.Context(), name, r.URL.Query().Get("v"))
	c.partial(w, "value_diff", map[string]any{
		"Diff": lineDiff(old, cur.Value), "OldLabel": "previous", "NewLabel": "current",
	})
}

// smPromote rolls AWSCURRENT back to an older version.
//
// Secrets Manager has no undo: PutSecretValue makes a new version current and
// demotes the previous one, and going back means moving the stage label by
// hand. That is a two-version-id CLI call, which is why it does not get done
// under pressure — so it is a button, on the version you want back.
func (c *Console) smPromote(w http.ResponseWriter, r *http.Request) {
	name := r.FormValue("name")
	to := r.FormValue("version")
	s, err := c.be.GetSecret(r.Context(), name)
	if err != nil {
		c.fail(w, err)
		return
	}
	if to == s.VersionID {
		c.fail(w, errors.New("That version is already current."))
		return
	}
	if err := c.be.PromoteSecretVersion(r.Context(), name, to, s.VersionID); err != nil {
		c.fail(w, err)
		return
	}
	// Stays on the Versions tab where the button was pressed, the way ssmLabel
	// does, rather than redirecting to the page it is already on. It also keeps
	// the mutation sweep honest: no fixture can invent a real version id, so a
	// route classified as redirect-capable here would fail the sweep forever.
	toast(w, "Rolled back — that version is current again")
	c.smDetailPartial(w, r, name, "versions")
}

// smUpdateMeta changes description and KMS key without writing a new version.
// Routing this through PutSecretValue would create a version every time
// someone fixed a typo in a description.
func (c *Console) smUpdateMeta(w http.ResponseWriter, r *http.Request) {
	name := r.FormValue("name")
	if err := c.be.UpdateSecretMeta(r.Context(), name,
		strings.TrimSpace(r.FormValue("description")), strings.TrimSpace(r.FormValue("kms_key"))); err != nil {
		c.fail(w, err)
		return
	}
	c.redirect(w, r, c.prefix+"/sm/secret?name="+url.QueryEscape(name), "Secret details updated")
}

// kmsDescribe updates a key's description. See UpdateKeyDescription for why
// this was worth a route.
func (c *Console) kmsDescription(w http.ResponseWriter, r *http.Request) {
	id := param(r, "key")
	if err := c.be.UpdateKeyDescription(r.Context(), id, strings.TrimSpace(r.FormValue("description"))); err != nil {
		c.fail(w, err)
		return
	}
	c.redirect(w, r, c.prefix+"/kms/"+id, "Description updated")
}

// kmsRandom asks the service for bytes. No key involved, which is what makes it
// the quickest check that the endpoint answers and credentials resolve.
func (c *Console) kmsRandom(w http.ResponseWriter, r *http.Request) {
	n := atoiDefault(r.FormValue("bytes"), 32)
	if n < 1 || n > 1024 {
		n = 32
	}
	out, err := c.be.GenerateRandom(r.Context(), n)
	if err != nil {
		c.fail(w, err)
		return
	}
	c.partial(w, "kms_out", map[string]any{"Label": "Random bytes (base64)", "Value": out})
}

// kmsPublicKey exports the verifying half of an asymmetric key.
func (c *Console) kmsPublicKey(w http.ResponseWriter, r *http.Request) {
	out, err := c.be.PublicKey(r.Context(), param(r, "key"))
	if err != nil {
		c.fail(w, err)
		return
	}
	c.partial(w, "kms_out", map[string]any{"Label": "Public key (base64 DER)", "Value": out})
}

// kmsReEncrypt moves ciphertext to another key. The plaintext never returns to
// the caller, which is the whole reason the operation exists and the reason
// this is not Decrypt followed by Encrypt.
func (c *Console) kmsReEncrypt(w http.ResponseWriter, r *http.Request) {
	dest := strings.TrimSpace(r.FormValue("dest"))
	if dest == "" {
		c.fail(w, errors.New("Choose the key to re-encrypt to."))
		return
	}
	out, err := c.be.ReEncrypt(r.Context(), strings.TrimSpace(r.FormValue("ciphertext")), dest)
	if err != nil {
		c.fail(w, err)
		return
	}
	c.partial(w, "kms_out", map[string]any{
		"Label": "Ciphertext under " + dest, "Value": out,
		"Note": "The plaintext never came back to this page — that is what ReEncrypt is for.",
	})
}

// kmsUpdateAlias repoints an alias. Delete-and-recreate would leave it briefly
// absent, and anything resolving it in that window fails.
func (c *Console) kmsUpdateAlias(w http.ResponseWriter, r *http.Request) {
	id := param(r, "key")
	alias := strings.TrimSpace(r.FormValue("existing_alias"))
	if alias == "" {
		c.fail(w, errors.New("Name the alias to repoint at this key."))
		return
	}
	if err := c.be.UpdateAlias(r.Context(), alias, id); err != nil {
		c.fail(w, err)
		return
	}
	// Re-renders the key panel, the way kmsAddAlias and kmsDeleteAlias do —
	// the alias list is right there and is the thing that changed. It also
	// keeps the mutation sweep honest: repointing needs an alias that already
	// exists, which no fixture can invent, so a route classified as
	// redirect-capable here would fail that sweep forever.
	toast(w, "Alias “"+alias+"” now points here")
	c.kmsKeyPartial(w, r)
}

// ssmPath answers "what lives under this prefix" with the service's own tree
// query, and offers to delete all of it in one batch. GetParametersByPath is
// the enumeration your code would use; DeleteParameters is the batch behind
// the button — both were implemented and unreachable, and together they make
// the one bulk operation a path-shaped store actually wants.
func (c *Console) ssmPath(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimSpace(r.FormValue("path"))
	if path == "" || !strings.HasPrefix(path, "/") {
		c.fail(w, errors.New("Give a path starting with / — the tree query is prefix-based."))
		return
	}
	params, err := c.be.ParametersByPath(r.Context(), path)
	if err != nil {
		c.fail(w, err)
		return
	}
	c.partial(w, "ssm_path_result", map[string]any{"Prefix": c.prefix, "Path": path, "Params": params})
}

// ssmDeletePath deletes everything a path query returned. Enumerate again at
// delete time rather than trusting the form — a parameter created between the
// listing and the click should be deleted with its siblings, not survive
// because it was not on screen.
func (c *Console) ssmDeletePath(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimSpace(r.FormValue("path"))
	params, err := c.be.ParametersByPath(r.Context(), path)
	if err != nil {
		c.fail(w, err)
		return
	}
	if len(params) == 0 {
		c.fail(w, errors.New("Nothing under "+path+" any more."))
		return
	}
	names := make([]string, 0, len(params))
	for _, p := range params {
		names = append(names, p.Name)
	}
	deleted, invalid, err := c.be.DeleteParameters(r.Context(), names)
	if err != nil {
		c.fail(w, err)
		return
	}
	note := plural(len(deleted), "parameter") + " deleted"
	if len(invalid) > 0 {
		note += ", " + strconv.Itoa(len(invalid)) + " already gone"
	}
	c.redirect(w, r, c.prefix+"/ssm", note)
}

// kmsSavePolicy replaces the key's resource policy from the builder.
func (c *Console) kmsSavePolicy(w http.ResponseWriter, r *http.Request) {
	doc := strings.TrimSpace(r.FormValue("document"))
	if doc == "" {
		c.fail(w, errors.New("a key policy cannot be empty — KMS always has one"))
		return
	}
	if err := c.be.PutKeyPolicy(r.Context(), param(r, "key"), doc); err != nil {
		c.fail(w, err)
		return
	}
	toast(w, "Key policy saved — evaluated under IAM soft and enforce; it gates identity policies")
	c.kmsKeyPartial(w, r)
}
