package console

import "net/http"

// ---- KMS ----

func (c *Console) kmsKeys(w http.ResponseWriter, r *http.Request) {
	keys, err := c.be.ListKeys(r.Context())
	if err != nil {
		c.fail(w, err)
		return
	}
	c.render(w, "kms_keys", map[string]any{"Keys": keys})
}

func (c *Console) kmsCreateKey(w http.ResponseWriter, r *http.Request) {
	if err := c.be.CreateKey(r.Context(),
		r.FormValue("spec"), r.FormValue("usage"),
		r.FormValue("alias"), r.FormValue("description")); err != nil {
		c.fail(w, err)
		return
	}
	toast(w, "Key created")
	keys, _ := c.be.ListKeys(r.Context())
	c.partial(w, "kms_key_list", map[string]any{"Keys": keys})
}

func (c *Console) kmsKey(w http.ResponseWriter, r *http.Request) {
	key, err := c.be.DescribeKey(r.Context(), r.PathValue("key"))
	if err != nil {
		c.fail(w, err)
		return
	}
	c.render(w, "kms_key", map[string]any{"Key": key})
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
	c.partial(w, "kms_crypto_result", map[string]any{"Label": "Ciphertext (base64)", "Value": out})
}

func (c *Console) kmsDecrypt(w http.ResponseWriter, r *http.Request) {
	out, err := c.be.KMSDecrypt(r.Context(), r.FormValue("ciphertext"))
	if err != nil {
		c.fail(w, err)
		return
	}
	c.partial(w, "kms_crypto_result", map[string]any{"Label": "Plaintext", "Value": out})
}

// ---- SSM Parameter Store ----

func (c *Console) ssmParams(w http.ResponseWriter, r *http.Request) {
	params, err := c.be.ListParameters(r.Context())
	if err != nil {
		c.fail(w, err)
		return
	}
	c.render(w, "ssm_params", map[string]any{"Params": params})
}

func (c *Console) ssmCreate(w http.ResponseWriter, r *http.Request) {
	name := r.FormValue("name")
	if err := c.be.PutParameter(r.Context(), name, r.FormValue("value"), r.FormValue("type"), false); err != nil {
		c.fail(w, err)
		return
	}
	toast(w, "Parameter “"+name+"” created")
	params, _ := c.be.ListParameters(r.Context())
	c.partial(w, "ssm_param_list", map[string]any{"Params": params})
}

func (c *Console) ssmParam(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("name")
	p, err := c.be.GetParameter(r.Context(), name)
	if err != nil {
		c.fail(w, err)
		return
	}
	hist, _ := c.be.ParameterHistory(r.Context(), name)
	c.render(w, "ssm_param", map[string]any{"P": p, "History": hist})
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
	c.render(w, "sm_secrets", map[string]any{"Secrets": secrets})
}

func (c *Console) smCreate(w http.ResponseWriter, r *http.Request) {
	name := r.FormValue("name")
	if err := c.be.CreateSecret(r.Context(), name, r.FormValue("value"), r.FormValue("description")); err != nil {
		c.fail(w, err)
		return
	}
	toast(w, "Secret “"+name+"” created")
	secrets, _ := c.be.ListSecrets(r.Context())
	c.partial(w, "sm_secret_list", map[string]any{"Secrets": secrets})
}

func (c *Console) smSecret(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("name")
	s, err := c.be.GetSecret(r.Context(), name)
	if err != nil {
		c.fail(w, err)
		return
	}
	c.render(w, "sm_secret", map[string]any{"S": s})
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
	c.partial(w, "sm_secret_detail", map[string]any{"S": s})
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
