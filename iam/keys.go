package iam

// Access keys and instance profiles.
//
// Access keys matter beyond CRUD: an access key id is how enforcement resolves
// a request back to a principal, so CreateAccessKey is what makes a user
// addressable from the request path at all.

import (
	"github.com/doze-dev/doze-aws/awsident"
	"github.com/doze-dev/doze-aws/internal/awshttp"
)

func hCreateAccessKey(s *Server, p params) (any, *awshttp.APIError) {
	user := p.str("UserName")
	if user == "" {
		return nil, errValidation("UserName is required (doze-aws has no implicit calling user)")
	}
	k, err := s.store.CreateAccessKey(user)
	if err != nil {
		return nil, awshttp.AsAPIError(err)
	}
	// The secret is returned once, as in AWS — though locally nothing verifies
	// it, since signatures are parsed and not checked.
	return struct {
		AccessKey accessKeyView `xml:"AccessKey"`
	}{accessKeyView{
		UserName: k.UserName, AccessKeyId: k.ID, SecretAccessKey: k.Secret,
		Status: k.Status, CreateDate: iso(k.Created),
	}}, nil
}

func hListAccessKeys(s *Server, p params) (any, *awshttp.APIError) {
	user := p.str("UserName")
	if _, err := s.store.GetUser(user); err != nil {
		return nil, awshttp.AsAPIError(err)
	}
	keys, err := s.store.ListAccessKeys(user)
	if err != nil {
		return nil, awshttp.AsAPIError(err)
	}
	views := make([]accessKeyView, 0, len(keys))
	for _, k := range keys {
		// The secret is deliberately absent from list responses, as in AWS.
		views = append(views, accessKeyView{
			UserName: k.UserName, AccessKeyId: k.ID, Status: k.Status, CreateDate: iso(k.Created),
		})
	}
	return struct {
		AccessKeyMetadata []accessKeyView `xml:"AccessKeyMetadata>member"`
		IsTruncated       bool            `xml:"IsTruncated"`
	}{views, false}, nil
}

func hUpdateAccessKey(s *Server, p params) (any, *awshttp.APIError) {
	status := p.str("Status")
	if status != "Active" && status != "Inactive" {
		return nil, errValidation("Status must be Active or Inactive")
	}
	return nil, awshttp.AsAPIErrorOrNil(s.store.UpdateAccessKey(p.str("AccessKeyId"), status))
}

func hDeleteAccessKey(s *Server, p params) (any, *awshttp.APIError) {
	return nil, awshttp.AsAPIErrorOrNil(s.store.DeleteAccessKey(p.str("AccessKeyId")))
}

func hGetAccessKeyLastUsed(s *Server, p params) (any, *awshttp.APIError) {
	id := p.str("AccessKeyId")
	k, ok := s.store.LookupAccessKey(id)
	if !ok {
		return nil, errNoEntity("The Access Key with id %s cannot be found.", id)
	}
	// Last-used data is real here, because the enforcement middleware records
	// it on every request that carries the key.
	used := struct {
		LastUsedDate string `xml:"LastUsedDate,omitempty"`
		ServiceName  string `xml:"ServiceName"`
		Region       string `xml:"Region"`
	}{ServiceName: "N/A", Region: "N/A"}
	if k.LastUsed != 0 {
		used.LastUsedDate = iso(k.LastUsed)
		used.ServiceName = k.LastSvc
		used.Region = awsident.Region
	}
	return struct {
		UserName          string `xml:"UserName"`
		AccessKeyLastUsed any    `xml:"AccessKeyLastUsed"`
	}{k.UserName, used}, nil
}

// ---- instance profiles ----

func hCreateInstanceProfile(s *Server, p params) (any, *awshttp.APIError) {
	prof, err := s.store.CreateInstanceProfile(p.str("InstanceProfileName"), p.str("Path"))
	if err != nil {
		return nil, awshttp.AsAPIError(err)
	}
	return struct {
		InstanceProfile instanceProfileView `xml:"InstanceProfile"`
	}{s.viewProfile(prof)}, nil
}

func hGetInstanceProfile(s *Server, p params) (any, *awshttp.APIError) {
	prof, err := s.store.GetInstanceProfile(p.str("InstanceProfileName"))
	if err != nil {
		return nil, awshttp.AsAPIError(err)
	}
	return struct {
		InstanceProfile instanceProfileView `xml:"InstanceProfile"`
	}{s.viewProfile(prof)}, nil
}

func hDeleteInstanceProfile(s *Server, p params) (any, *awshttp.APIError) {
	return nil, awshttp.AsAPIErrorOrNil(s.store.DeleteInstanceProfile(p.str("InstanceProfileName")))
}

func hListInstanceProfiles(s *Server, _ params) (any, *awshttp.APIError) {
	profs, err := s.store.ListInstanceProfiles()
	if err != nil {
		return nil, awshttp.AsAPIError(err)
	}
	views := make([]instanceProfileView, 0, len(profs))
	for i := range profs {
		views = append(views, s.viewProfile(&profs[i]))
	}
	return struct {
		InstanceProfiles []instanceProfileView `xml:"InstanceProfiles>member"`
		IsTruncated      bool                  `xml:"IsTruncated"`
	}{views, false}, nil
}

func hListInstanceProfilesForRole(s *Server, p params) (any, *awshttp.APIError) {
	role := p.str("RoleName")
	profs, err := s.store.ListInstanceProfiles()
	if err != nil {
		return nil, awshttp.AsAPIError(err)
	}
	views := make([]instanceProfileView, 0)
	for i := range profs {
		if containsString(profs[i].Roles, role) {
			views = append(views, s.viewProfile(&profs[i]))
		}
	}
	return struct {
		InstanceProfiles []instanceProfileView `xml:"InstanceProfiles>member"`
		IsTruncated      bool                  `xml:"IsTruncated"`
	}{views, false}, nil
}

func hAddRoleToInstanceProfile(s *Server, p params) (any, *awshttp.APIError) {
	role := p.str("RoleName")
	if _, err := s.store.GetRole(role); err != nil {
		return nil, awshttp.AsAPIError(err)
	}
	err := s.store.UpdateInstanceProfile(p.str("InstanceProfileName"), func(prof *InstanceProfile) error {
		// AWS allows exactly one role per instance profile.
		if len(prof.Roles) > 0 && !containsString(prof.Roles, role) {
			return errLimitExceeded("Cannot exceed quota for InstanceSessionsPerInstanceProfile: 1")
		}
		if !containsString(prof.Roles, role) {
			prof.Roles = append(prof.Roles, role)
		}
		return nil
	})
	return nil, awshttp.AsAPIErrorOrNil(err)
}

func hRemoveRoleFromInstanceProfile(s *Server, p params) (any, *awshttp.APIError) {
	role := p.str("RoleName")
	err := s.store.UpdateInstanceProfile(p.str("InstanceProfileName"), func(prof *InstanceProfile) error {
		prof.Roles = removeString(prof.Roles, role)
		return nil
	})
	return nil, awshttp.AsAPIErrorOrNil(err)
}

func hTagInstanceProfile(s *Server, p params) (any, *awshttp.APIError) {
	err := s.store.UpdateInstanceProfile(p.str("InstanceProfileName"), func(prof *InstanceProfile) error {
		prof.Tags = mergeTags(prof.Tags, p.tags())
		return nil
	})
	return nil, awshttp.AsAPIErrorOrNil(err)
}

func hUntagInstanceProfile(s *Server, p params) (any, *awshttp.APIError) {
	err := s.store.UpdateInstanceProfile(p.str("InstanceProfileName"), func(prof *InstanceProfile) error {
		for _, k := range p.members("TagKeys") {
			delete(prof.Tags, k)
		}
		return nil
	})
	return nil, awshttp.AsAPIErrorOrNil(err)
}

func hListInstanceProfileTags(s *Server, p params) (any, *awshttp.APIError) {
	prof, err := s.store.GetInstanceProfile(p.str("InstanceProfileName"))
	if err != nil {
		return nil, awshttp.AsAPIError(err)
	}
	return struct {
		Tags        []tagView `xml:"Tags>member"`
		IsTruncated bool      `xml:"IsTruncated"`
	}{tagViews(prof.Tags), false}, nil
}

func (s *Server) viewProfile(prof *InstanceProfile) instanceProfileView {
	var roles []roleView
	for _, name := range prof.Roles {
		if r, err := s.store.GetRole(name); err == nil {
			roles = append(roles, viewRole(r))
		}
	}
	return instanceProfileView{
		Path: prof.Path, InstanceProfileName: prof.Name, InstanceProfileId: prof.ID,
		Arn:        awsident.GlobalARN("iam", "instance-profile"+prof.Path+prof.Name),
		CreateDate: iso(prof.Created), Roles: roles,
	}
}
