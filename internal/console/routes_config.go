package console

// Routes: KMS, Secrets Manager and Parameter Store.

import "github.com/go-chi/chi/v5"

// KMS.
func (c *Console) kmsRoutes(r chi.Router) {
	r.Get("/create", c.createPage("kms", "kms_create"))
	r.Get("/", c.kmsKeys)
	r.Post("/create", c.kmsCreateKey)
	r.Get("/{key}", c.kmsKey)
	r.Post("/{key}/toggle-enabled", c.kmsToggleEnabled)
	r.Post("/{key}/policy", c.kmsSavePolicy) // HTMX partial (PutKeyPolicy)
	r.Post("/{key}/toggle-rotation", c.kmsToggleRotation)
	r.Post("/{key}/rotate-now", c.kmsRotateNow)
	r.Post("/{key}/schedule-deletion", c.kmsScheduleDeletion)
	r.Post("/{key}/encrypt", c.kmsEncrypt)
	r.Post("/{key}/decrypt", c.kmsDecrypt)
	r.Post("/{key}/sign", c.kmsSign)
	r.Post("/{key}/verify", c.kmsVerify)
	r.Post("/{key}/mac", c.kmsMac)
	r.Post("/{key}/verify-mac", c.kmsVerifyMac)
	r.Post("/{key}/add-alias", c.kmsAddAlias)
	r.Post("/{key}/description", c.kmsDescription)  // UpdateKeyDescription
	r.Post("/random", c.kmsRandom)                  // GenerateRandom — no key needed
	r.Post("/{key}/public-key", c.kmsPublicKey)     // GetPublicKey
	r.Post("/{key}/reencrypt", c.kmsReEncrypt)      // ReEncrypt
	r.Post("/{key}/update-alias", c.kmsUpdateAlias) // UpdateAlias
	r.Post("/{key}/delete-alias", c.kmsDeleteAlias)
	r.Post("/{key}/cancel-deletion", c.kmsCancelDeletion)
}

// Secrets Manager.
func (c *Console) smRoutes(r chi.Router) {
	r.Get("/create", c.createPage("sm", "sm_create"))
	// Secrets Manager (names may contain slashes -> query params).
	r.Get("/", c.smSecrets)
	r.Post("/create", c.smCreate)
	r.Post("/restore", c.smRestore)
	r.Post("/promote", c.smPromote)   // UpdateSecretVersionStage — the rollback
	r.Post("/update", c.smUpdateMeta) // UpdateSecret
	r.Post("/rotation", c.smConfigureRotation)
	r.Post("/rotate-now", c.smRotateNow)
	r.Post("/policy", c.smSavePolicy) // PutResourcePolicy / DeleteResourcePolicy
	r.Get("/password", c.smPassword)
	r.Get("/secret", c.smSecret)
	r.Get("/diff", c.smDiff)
	r.Post("/put", c.smPut)
	r.Post("/delete", c.smDelete)
}

// SSM Parameter Store.
func (c *Console) ssmRoutes(r chi.Router) {
	r.Get("/create", c.createPage("ssm", "ssm_create"))
	// SSM Parameter Store (names contain slashes -> query params).
	r.Get("/", c.ssmParams)
	r.Post("/create", c.ssmCreate)
	r.Get("/param", c.ssmParam)
	r.Get("/diff", c.ssmDiff)
	r.Post("/put", c.ssmPut)
	r.Post("/delete", c.ssmDelete)
	r.Post("/label", c.ssmLabel)
	r.Post("/unlabel", c.ssmUnlabel)        // UnlabelParameterVersion
	r.Post("/path", c.ssmPath)              // GetParametersByPath
	r.Post("/delete-path", c.ssmDeletePath) // DeleteParameters
}
