package console

import (
	"net/http"
	"net/url"
	"strconv"
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
	key, err := c.be.DescribeKey(r.Context(), r.PathValue("key"))
	if err != nil {
		c.fail(w, err)
		return
	}
	keys, _ := c.be.ListKeys(r.Context())
	c.render(w, r, "kms_key", map[string]any{"Key": key, "List": keys, "Title": "KMS"})
}

func (c *Console) kmsKeyPartial(w http.ResponseWriter, r *http.Request) {
	key, err := c.be.DescribeKey(r.Context(), r.PathValue("key"))
	if err != nil {
		c.fail(w, err)
		return
	}
	c.partial(w, "kms_key_detail", map[string]any{"Key": key})
}

func (c *Console) kmsToggleEnabled(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("key")
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
	id := r.PathValue("key")
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
	if err := c.be.RotateKeyNow(r.Context(), r.PathValue("key")); err != nil {
		c.fail(w, err)
		return
	}
	toast(w, "Key material rotated")
	c.kmsKeyPartial(w, r)
}

func (c *Console) kmsScheduleDeletion(w http.ResponseWriter, r *http.Request) {
	if err := c.be.ScheduleKeyDeletion(r.Context(), r.PathValue("key")); err != nil {
		c.fail(w, err)
		return
	}
	toast(w, "Deletion scheduled (7 days)")
	w.Header().Set("HX-Redirect", c.prefix+"/kms")
}

func (c *Console) kmsEncrypt(w http.ResponseWriter, r *http.Request) {
	out, err := c.be.KMSEncrypt(r.Context(), r.PathValue("key"), r.FormValue("plaintext"))
	if err != nil {
		c.fail(w, err)
		return
	}
	c.partial(w, "kms_crypto_result", map[string]any{
		"Label": "Ciphertext (base64)", "Value": out,
		// One-click round-trip: the result card can decrypt itself.
		"DecryptURL": c.prefix + "/kms/" + r.PathValue("key") + "/decrypt",
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
	key := r.PathValue("key")
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
	err := c.be.KMSVerify(r.Context(), r.PathValue("key"), r.FormValue("algo"), r.FormValue("message"), r.FormValue("signature"))
	c.partial(w, "kms_verdict", map[string]any{"Valid": err == nil, "What": "signature"})
}

// kmsMac / kmsVerifyMac — HMAC playground.
func (c *Console) kmsMac(w http.ResponseWriter, r *http.Request) {
	key := r.PathValue("key")
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
	err := c.be.KMSVerifyMac(r.Context(), r.PathValue("key"), r.FormValue("algo"), r.FormValue("message"), r.FormValue("mac"))
	c.partial(w, "kms_verdict", map[string]any{"Valid": err == nil, "What": "MAC"})
}

func (c *Console) kmsAddAlias(w http.ResponseWriter, r *http.Request) {
	key := r.PathValue("key")
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
	if err := c.be.KMSCancelDeletion(r.Context(), r.PathValue("key")); err != nil {
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
	hist, _ := c.be.ParameterHistory(r.Context(), name)
	all, _ := c.be.ListParameters(r.Context())
	c.render(w, r, "ssm_param", map[string]any{"P": p, "History": hist, "List": all, "Sel": name, "Mode": tabOf(r, "view"), "Secure": p.Type == "SecureString", "Title": p.Name + " · Parameter Store"})
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
	np, _ := c.be.GetParameter(r.Context(), name)
	hist, _ := c.be.ParameterHistory(r.Context(), name)
	c.partial(w, "ssm_param_detail", map[string]any{"P": np, "History": hist})
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
	np, _ := c.be.GetParameter(r.Context(), name)
	hist, _ := c.be.ParameterHistory(r.Context(), name)
	// Keep the user on the Versions tab where they submitted the label.
	c.partial(w, "ssm_param_detail", map[string]any{"P": np, "History": hist, "Mode": "versions"})
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

func (c *Console) smSecret(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("name")
	s, err := c.be.GetSecret(r.Context(), name)
	if err != nil {
		c.fail(w, err)
		return
	}
	all, _ := c.be.ListSecrets(r.Context())
	fns, _ := c.be.ListFunctions(r.Context())
	c.render(w, r, "sm_secret", map[string]any{"S": s, "List": all, "Functions": fns, "Sel": name, "Mode": tabOf(r, "view"), "Title": s.Name + " · Secrets Manager"})
}

// smRotationPartial re-renders the secret detail (rotation strip lives there).
func (c *Console) smRotationRefresh(w http.ResponseWriter, r *http.Request, name string) {
	s, err := c.be.GetSecret(r.Context(), name)
	if err != nil {
		c.fail(w, err)
		return
	}
	fns, _ := c.be.ListFunctions(r.Context())
	c.partial(w, "sm_secret_detail", map[string]any{"S": s, "Functions": fns})
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
	s, err := c.be.GetSecret(r.Context(), name)
	if err != nil {
		c.fail(w, err)
		return
	}
	fns, _ := c.be.ListFunctions(r.Context())
	c.partial(w, "sm_secret_detail", map[string]any{"S": s, "Functions": fns})
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
