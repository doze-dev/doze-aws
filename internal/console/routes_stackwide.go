package console

// Routes: IAM and CloudFormation.

import "github.com/go-chi/chi/v5"

// IAM.
func (c *Console) iamRoutes(r chi.Router) {
	r.Get("/", c.iamHome)
	r.Get("/policy", c.iamPolicy)
	r.Get("/{kind}/{name}", c.iamPrincipal)
	r.Post("/simulate", c.iamSimulate)
	r.Post("/simulate-inline", c.iamSimInline) // HTMX partial (the builder's live check)
	r.Post("/generate", c.iamGenerate)
	r.Get("/create", c.iamCreatePage)
	r.Get("/account", c.iamAccount)
	r.Get("/sts", c.iamSTS)
	r.Post("/sts/mint", c.iamSTSMint)        // HTMX partial (AssumeRole and friends)
	r.Post("/sts/key-info", c.iamSTSKeyInfo) // HTMX partial (GetAccessKeyInfo)
	r.Post("/account/alias", c.iamAlias)
	r.Post("/account/details", c.iamAuthDetails) // HTMX partial (GetAccountAuthorizationDetails)
	r.Post("/policy/new-version", c.iamNewPolicyVersion)
	r.Post("/policy/set-default", c.iamSetDefaultVersion)
	r.Post("/policy/delete-version", c.iamDeleteVersion)
	r.Post("/group/{name}/member", c.iamGroupMember)
	r.Post("/group/{name}/rename", c.iamGroupRename)
	r.Post("/profile/{name}/role", c.iamProfileRole)
	r.Post("/user/{name}/keys/toggle", c.iamKeyToggle)
	r.Post("/user/{name}/rename", c.iamRenameUser)
	r.Post("/user/{name}/join-group", c.iamJoinGroup)
	r.Post("/role/{name}/trust", c.iamTrust)
	r.Post("/role/{name}/meta", c.iamRoleMeta)
	r.Post("/create", c.iamCreate)
	r.Post("/policy/delete", c.iamDeletePolicy)
	r.Post("/{kind}/{name}/attach", c.iamAttach)
	r.Post("/{kind}/{name}/detach", c.iamDetach)
	r.Post("/{kind}/{name}/delete", c.iamDeletePrincipal)
	r.Post("/{kind}/{name}/inline", c.iamPutInline)
	r.Post("/{kind}/{name}/inline/delete", c.iamDeleteInline)
	r.Post("/user/{name}/keys", c.iamNewKey)
	r.Post("/user/{name}/keys/delete", c.iamDeleteKey)
}

// CloudFormation.
func (c *Console) cfnRoutes(r chi.Router) {
	r.Get("/", c.cfnStacks)
	r.Get("/create", c.createPage("cfn", "cfn_create"))
	r.Get("/export-template", c.cfnExportTemplate) // seeds the create page from what is running
	r.Post("/create", c.cfnCreate)
	r.Post("/validate", c.cfnValidate) // HTMX partial (ValidateTemplate)
	r.Post("/summary", c.cfnSummary)   // HTMX partial (GetTemplateSummary)
	r.Get("/{stack}", c.cfnStack)
	r.Post("/{stack}/delete", c.cfnDelete)
	r.Post("/{stack}/update", c.cfnUpdate)
	r.Post("/{stack}/changeset/{cs}/execute", c.cfnExecuteCS)
	r.Post("/{stack}/changeset/{cs}/delete", c.cfnDeleteCS)
	r.Post("/{stack}/resource", c.cfnResource) // HTMX partial (DescribeStackResource)
}
