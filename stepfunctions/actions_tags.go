package stepfunctions

import (
	"context"

	"github.com/doze-dev/doze-aws/internal/awshttp"
	"github.com/doze-dev/doze-aws/internal/awsjson"
)

// Tagging. Keyed by resource ARN and shared by state machines and activities,
// because the operations take an ARN and do not care what it points at.

func (s *Server) tagResource(ctx context.Context, p map[string]any) (any, *awshttp.APIError) {
	arn := awsjson.Str(p, "resourceArn")
	if arn == "" {
		return nil, errInvalidARN(arn)
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
	if arn == "" {
		return nil, errInvalidARN(arn)
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
	if arn == "" {
		return nil, errInvalidARN(arn)
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
