package provision

// S3 access settings: public access block, ownership controls and the
// bucket policy, applied and exported together so a round trip keeps them.

import (
	"context"
	"fmt"
	"strconv"
	"strings"
)

func publicAccessXML(p PublicAccessBlock) []byte {
	flag := func(name string, v bool) string { return "<" + name + ">" + strconv.FormatBool(v) + "</" + name + ">" }
	return []byte(`<PublicAccessBlockConfiguration xmlns="http://s3.amazonaws.com/doc/2006-03-01/">` +
		flag("BlockPublicAcls", p.BlockPublicAcls) + flag("IgnorePublicAcls", p.IgnorePublicAcls) +
		flag("BlockPublicPolicy", p.BlockPublicPolicy) + flag("RestrictPublicBuckets", p.RestrictPublicBuckets) +
		`</PublicAccessBlockConfiguration>`)
}

func parsePublicAccessXML(x string) *PublicAccessBlock {
	if !strings.Contains(x, "PublicAccessBlockConfiguration") {
		return nil
	}
	flag := func(name string) bool { return xmlValue(x, name) == "true" }
	return &PublicAccessBlock{
		BlockPublicAcls: flag("BlockPublicAcls"), IgnorePublicAcls: flag("IgnorePublicAcls"),
		BlockPublicPolicy: flag("BlockPublicPolicy"), RestrictPublicBuckets: flag("RestrictPublicBuckets"),
	}
}

func ownershipXML(setting string) []byte {
	return []byte(`<OwnershipControls xmlns="http://s3.amazonaws.com/doc/2006-03-01/"><Rule><ObjectOwnership>` +
		setting + `</ObjectOwnership></Rule></OwnershipControls>`)
}

// applyBucketAccess puts the access settings, block before policy: a public
// policy under BlockPublicPolicy is refused by S3, which is the outcome the
// template asked for.
func applyBucketAccess(ctx context.Context, c *client, name string, b Bucket) error {
	if b.PublicAccess != nil {
		if _, err := c.do(ctx, "PUT", "/"+name+"?publicAccessBlock", nil, publicAccessXML(*b.PublicAccess)); err != nil {
			return fmt.Errorf("bucket %q public access block: %w", name, err)
		}
	}
	if b.Ownership != "" {
		if _, err := c.do(ctx, "PUT", "/"+name+"?ownershipControls", nil, ownershipXML(b.Ownership)); err != nil {
			return fmt.Errorf("bucket %q ownership controls: %w", name, err)
		}
	}
	if !b.Policy.IsZero() {
		if _, err := c.do(ctx, "PUT", "/"+name+"?policy", map[string]string{"Content-Type": "application/json"}, []byte(b.Policy.JSON)); err != nil {
			return fmt.Errorf("bucket %q policy: %w", name, err)
		}
	}
	return nil
}

// exportBucketAccess reads the three settings back. The public access block
// is exported only when it differs from the default every bucket is born
// with, so an export of an untouched bucket stays quiet.
func exportBucketAccess(ctx context.Context, c *client, name string, b *Bucket) {
	if out, err := c.do(ctx, "GET", "/"+name+"?publicAccessBlock", nil, nil); err == nil {
		if p := parsePublicAccessXML(string(out)); p != nil && *p != (PublicAccessBlock{true, true, true, true}) {
			b.PublicAccess = p
		}
	}
	if out, err := c.do(ctx, "GET", "/"+name+"?ownershipControls", nil, nil); err == nil {
		b.Ownership = xmlValue(string(out), "ObjectOwnership")
	}
	if out, err := c.do(ctx, "GET", "/"+name+"?policy", nil, nil); err == nil && len(out) > 0 {
		b.Policy = Doc{JSON: string(out)}
	}
}
