package stepfunctions

import (
	"context"

	"github.com/doze-dev/doze-aws/internal/awshttp"
	"github.com/doze-dev/doze-aws/internal/awsjson"
)

// Tagging. Keyed by resource ARN and shared by state machines and activities,
// because the operations take an ARN and do not care what it points at —
// beyond its existing. AWS answers ResourceNotFound for a tag operation on
// an ARN it does not hold, and so does this; before it did, listing the tags
// of a machine that was never created answered an empty list, which is the
// kind of silent success a deploy tool's read-back cannot tell from truth.

// taggable resolves a resource ARN to something that exists, or the error
// the tag operations answer when it does not.
func (s *Server) taggable(arn string) *awshttp.APIError {
	if name := nameFromARN(arn, "stateMachine"); name != "" {
		m, aerr := s.store.GetMachine(name)
		if aerr != nil {
			return aerr
		}
		if m == nil {
			return errResourceNotFound(arn)
		}
		return nil
	}
	if name := nameFromARN(arn, "activity"); name != "" {
		a, aerr := s.store.GetActivity(name)
		if aerr != nil {
			return aerr
		}
		if a == nil {
			return errResourceNotFound(arn)
		}
		return nil
	}
	return errInvalidARN(arn)
}

func (s *Server) tagResource(ctx context.Context, p map[string]any) (any, *awshttp.APIError) {
	arn := awsjson.Str(p, "resourceArn")
	if aerr := s.taggable(arn); aerr != nil {
		return nil, aerr
	}
	existing, err := s.store.tags(arn)
	if err != nil {
		return nil, asAPIError(err)
	}
	for k, v := range tagsOf(p) {
		existing[k] = v
	}
	if err := s.store.setTags(arn, existing); err != nil {
		return nil, asAPIError(err)
	}
	return map[string]any{}, nil
}

func (s *Server) untagResource(ctx context.Context, p map[string]any) (any, *awshttp.APIError) {
	arn := awsjson.Str(p, "resourceArn")
	if aerr := s.taggable(arn); aerr != nil {
		return nil, aerr
	}
	existing, err := s.store.tags(arn)
	if err != nil {
		return nil, asAPIError(err)
	}
	for _, k := range awsjson.Strs(p, "tagKeys") {
		delete(existing, k)
	}
	if err := s.store.setTags(arn, existing); err != nil {
		return nil, asAPIError(err)
	}
	return map[string]any{}, nil
}

func (s *Server) listTagsForResource(ctx context.Context, p map[string]any) (any, *awshttp.APIError) {
	arn := awsjson.Str(p, "resourceArn")
	if aerr := s.taggable(arn); aerr != nil {
		return nil, aerr
	}
	existing, err := s.store.tags(arn)
	if err != nil {
		return nil, asAPIError(err)
	}
	items := make([]any, 0, len(existing))
	for k, v := range existing {
		items = append(items, map[string]any{"key": k, "value": v})
	}
	return map[string]any{"tags": items}, nil
}
