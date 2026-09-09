// Regressions from the post-batch audit of resource policies: each test
// reproduces a bypass or an over-deny the review found, under enforce.
package iam_test

import (
	"context"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awskms "github.com/aws/aws-sdk-go-v2/service/kms"
	awslambda "github.com/aws/aws-sdk-go-v2/service/lambda"
	lamtypes "github.com/aws/aws-sdk-go-v2/service/lambda/types"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	awssns "github.com/aws/aws-sdk-go-v2/service/sns"
	snstypes "github.com/aws/aws-sdk-go-v2/service/sns/types"
	awssqs "github.com/aws/aws-sdk-go-v2/service/sqs"
	sqstypes "github.com/aws/aws-sdk-go-v2/service/sqs/types"

	"github.com/doze-dev/doze-aws/awsident"
	"github.com/doze-dev/doze-aws/iam"
)

// Decrypt names its key inside the ciphertext, ReEncrypt names two, the
// alias operations name a target: every one consults the key policy. And a
// key policy that scopes its root statement to the key's own ARN opens the
// gate exactly as the default "*" one does.
func TestKeyPolicyCoversDecryptReEncryptAndAliases(t *testing.T) {
	ctx := context.Background()
	root, endpoint := stackWith(t, iam.ModeEnforce, "iam", "kms")
	rootKMS := kmsClient(rootCfg(), endpoint)
	bob := kmsClient(userConfig(t, root, "bob", allowAll), endpoint)

	key, _ := rootKMS.CreateKey(ctx, &awskms.CreateKeyInput{})
	keyID := aws.ToString(key.KeyMetadata.KeyId)
	other, _ := rootKMS.CreateKey(ctx, &awskms.CreateKeyInput{})
	ct, err := rootKMS.Encrypt(ctx, &awskms.EncryptInput{KeyId: aws.String(keyID), Plaintext: []byte("secret")})
	if err != nil {
		t.Fatal(err)
	}
	// The default root clause, scoped to the key ARN rather than "*", plus a
	// Deny for bob on the crypto actions.
	policy := `{"Version":"2012-10-17","Statement":[
		{"Sid":"Enable IAM policies","Effect":"Allow","Principal":{"AWS":"` + awsident.GlobalARN("iam", "root") + `"},"Action":"kms:*","Resource":"` + aws.ToString(key.KeyMetadata.Arn) + `"},
		{"Sid":"NoBob","Effect":"Deny","Principal":{"AWS":"` + userARN("bob") + `"},"Action":["kms:Encrypt","kms:Decrypt","kms:ReEncryptFrom","kms:CreateAlias"],"Resource":"*"}]}`
	if _, err := rootKMS.PutKeyPolicy(ctx, &awskms.PutKeyPolicyInput{KeyId: aws.String(keyID), PolicyName: aws.String("default"), Policy: aws.String(policy)}); err != nil {
		t.Fatal(err)
	}
	// The key-scoped root clause keeps the gate open for the root caller.
	if _, err := rootKMS.Encrypt(ctx, &awskms.EncryptInput{KeyId: aws.String(keyID), Plaintext: []byte("x")}); err != nil {
		t.Fatalf("a root clause scoped to the key ARN must open the gate: %v", err)
	}
	_, err = bob.Decrypt(ctx, &awskms.DecryptInput{CiphertextBlob: ct.CiphertextBlob})
	wantDenied(t, err, "Decrypt names the key through the ciphertext", "kms:Decrypt", "NoBob")
	_, err = bob.ReEncrypt(ctx, &awskms.ReEncryptInput{CiphertextBlob: ct.CiphertextBlob, DestinationKeyId: other.KeyMetadata.KeyId})
	wantDenied(t, err, "ReEncrypt consults the source key", "kms:ReEncryptFrom", "NoBob")
	_, err = bob.CreateAlias(ctx, &awskms.CreateAliasInput{AliasName: aws.String("alias/bobs"), TargetKeyId: aws.String(keyID)})
	wantDenied(t, err, "CreateAlias consults the target key", "kms:CreateAlias", "NoBob")
	// Not denied: the same calls on the other key, and Decrypt by root.
	if _, err := bob.CreateAlias(ctx, &awskms.CreateAliasInput{AliasName: aws.String("alias/bobs"), TargetKeyId: other.KeyMetadata.KeyId}); err != nil {
		t.Fatalf("CreateAlias on the other key: %v", err)
	}
	if _, err := rootKMS.Decrypt(ctx, &awskms.DecryptInput{CiphertextBlob: ct.CiphertextBlob}); err != nil {
		t.Fatalf("root Decrypt: %v", err)
	}
	// A key named by alias is authorized on the key, not the alias ARN:
	// an identity policy scoped to the key ARN admits it.
	scoped := kmsClient(userConfig(t, root, "scoped",
		`{"Statement":[{"Effect":"Allow","Action":"kms:*","Resource":"`+aws.ToString(other.KeyMetadata.Arn)+`"}]}`), endpoint)
	if _, err := scoped.Encrypt(ctx, &awskms.EncryptInput{KeyId: aws.String("alias/bobs"), Plaintext: []byte("x")}); err != nil {
		t.Fatalf("an alias-named request must be authorized on the key ARN: %v", err)
	}
}

// The batch operations are authorized as the action they batch.
func TestBatchActionsAreTheirBaseAction(t *testing.T) {
	ctx := context.Background()
	root, endpoint := stackWith(t, iam.ModeEnforce, "iam", "sqs", "sns")
	rootSQS, rootSNS := sqsClient(rootCfg(), endpoint), snsClient(rootCfg(), endpoint)
	q, _ := rootSQS.CreateQueue(ctx, &awssqs.CreateQueueInput{QueueName: aws.String("batched")})
	top, _ := rootSNS.CreateTopic(ctx, &awssns.CreateTopicInput{Name: aws.String("batched")})
	rootSQS.SetQueueAttributes(ctx, &awssqs.SetQueueAttributesInput{QueueUrl: q.QueueUrl, Attributes: map[string]string{
		"Policy": `{"Version":"2012-10-17","Statement":[{"Sid":"NoWriter","Effect":"Deny","Principal":{"AWS":"` + userARN("writer") + `"},"Action":"sqs:SendMessage","Resource":"*"}]}`}})
	rootSNS.SetTopicAttributes(ctx, &awssns.SetTopicAttributesInput{TopicArn: top.TopicArn, AttributeName: aws.String("Policy"), AttributeValue: aws.String(
		`{"Version":"2012-10-17","Statement":[{"Sid":"NoWriter","Effect":"Deny","Principal":{"AWS":"` + userARN("writer") + `"},"Action":"sns:Publish","Resource":"*"}]}`)})
	cfg := userConfig(t, root, "writer", allowAll)
	writer, writerSNS := sqsClient(cfg, endpoint), snsClient(cfg, endpoint)
	_, err := writer.SendMessageBatch(ctx, &awssqs.SendMessageBatchInput{QueueUrl: q.QueueUrl,
		Entries: []sqstypes.SendMessageBatchRequestEntry{{Id: aws.String("1"), MessageBody: aws.String("hi")}}})
	wantDenied(t, err, "SendMessageBatch under a SendMessage deny", "sqs:SendMessage", "NoWriter")
	_, err = writerSNS.PublishBatch(ctx, &awssns.PublishBatchInput{TopicArn: top.TopicArn,
		PublishBatchRequestEntries: []snstypes.PublishBatchRequestEntry{{Id: aws.String("1"), Message: aws.String("hi")}}})
	wantDenied(t, err, "PublishBatch under a Publish deny", "sns:Publish", "NoWriter")
	// And an identity policy on the base action admits the batch.
	sender := sqsClient(userConfig(t, root, "sender", `{"Statement":[{"Effect":"Allow","Action":"sqs:SendMessage","Resource":"*"}]}`), endpoint)
	if _, err := sender.SendMessageBatch(ctx, &awssqs.SendMessageBatchInput{QueueUrl: q.QueueUrl,
		Entries: []sqstypes.SendMessageBatchRequestEntry{{Id: aws.String("1"), MessageBody: aws.String("hi")}}}); err != nil {
		t.Fatalf("sqs:SendMessage must cover SendMessageBatch: %v", err)
	}
}

// Virtual-host addressing and a copy's source are authorized for what they
// are, not for what the path alone suggests.
func TestS3VirtualHostAndCopySourceAreAuthorized(t *testing.T) {
	ctx := context.Background()
	root, endpoint := stackWith(t, iam.ModeEnforce, "iam", "s3")
	rootS3 := s3Client(rootCfg(), endpoint)
	for _, b := range []string{"vault", "public"} {
		rootS3.CreateBucket(ctx, &awss3.CreateBucketInput{Bucket: aws.String(b)})
	}
	rootS3.PutObject(ctx, &awss3.PutObjectInput{Bucket: aws.String("vault"), Key: aws.String("secret.txt"), Body: strings.NewReader("top secret")})

	// A user who may only list, addressing the object through the host.
	lister := awss3.NewFromConfig(userConfig(t, root, "lister",
		`{"Statement":[{"Effect":"Allow","Action":["s3:ListBucket","s3:ListAllMyBuckets"],"Resource":"*"}]}`),
		func(o *awss3.Options) { o.BaseEndpoint = aws.String(endpoint); o.UsePathStyle = false })
	_, err := lister.GetObject(ctx, &awss3.GetObjectInput{Bucket: aws.String("vault"), Key: aws.String("secret.txt")})
	wantDenied(t, err, "virtual-host GetObject by a lister", "s3:GetObject")
	if _, err := lister.ListObjectsV2(ctx, &awss3.ListObjectsV2Input{Bucket: aws.String("vault")}); err != nil {
		t.Fatalf("virtual-host ListBucket by a lister: %v", err)
	}

	// A user who may write the public bucket but not read the vault.
	copier := s3Client(userConfig(t, root, "copier",
		`{"Statement":[{"Effect":"Allow","Action":"s3:PutObject","Resource":"arn:aws:s3:::public/*"}]}`), endpoint)
	_, err = copier.CopyObject(ctx, &awss3.CopyObjectInput{Bucket: aws.String("public"), Key: aws.String("leak.txt"), CopySource: aws.String("vault/secret.txt")})
	wantDenied(t, err, "copying a source the identity may not read", "s3:GetObject")
	if _, err := rootS3.HeadObject(ctx, &awss3.HeadObjectInput{Bucket: aws.String("public"), Key: aws.String("leak.txt")}); err == nil {
		t.Fatal("the refused copy wrote the object")
	}
	reader := s3Client(userConfig(t, root, "reader",
		`{"Statement":[{"Effect":"Allow","Action":["s3:PutObject","s3:GetObject"],"Resource":"*"}]}`), endpoint)
	if _, err := reader.CopyObject(ctx, &awss3.CopyObjectInput{Bucket: aws.String("public"), Key: aws.String("ok.txt"), CopySource: aws.String("vault/secret.txt")}); err != nil {
		t.Fatalf("a copy the identity may make: %v", err)
	}
}

// A function referenced by ARN or with a qualifier is authorized on the
// function, and its resource policy is consulted.
func TestLambdaReferencesByARNAndQualifier(t *testing.T) {
	ctx := context.Background()
	root, endpoint := stackWith(t, iam.ModeEnforce, "iam", "lambda")
	rootLam := awslambda.NewFromConfig(rootCfg(), func(o *awslambda.Options) { o.BaseEndpoint = aws.String(endpoint) })
	fn, err := rootLam.CreateFunction(ctx, &awslambda.CreateFunctionInput{
		FunctionName: aws.String("fn"), Runtime: lamtypes.RuntimeProvidedal2, Handler: aws.String("bootstrap"),
		Role: aws.String("arn:aws:iam::000000000000:role/r"), Code: &lamtypes.FunctionCode{ZipFile: []byte("PK\x05\x06" + strings.Repeat("\x00", 18))},
	})
	if err != nil {
		t.Fatal(err)
	}
	carol := awslambda.NewFromConfig(userConfig(t, root, "carol",
		`{"Statement":[{"Effect":"Allow","Action":"lambda:*","Resource":"`+aws.ToString(fn.FunctionArn)+`*"}]}`),
		func(o *awslambda.Options) { o.BaseEndpoint = aws.String(endpoint) })
	if _, err := carol.GetFunction(ctx, &awslambda.GetFunctionInput{FunctionName: fn.FunctionArn}); err != nil {
		t.Fatalf("GetFunction by full ARN: %v", err)
	}
	if _, err := carol.GetFunction(ctx, &awslambda.GetFunctionInput{FunctionName: aws.String("fn:$LATEST")}); err != nil {
		t.Fatalf("GetFunction by qualified name: %v", err)
	}
	// The function's own policy still applies to a qualified reference.
	if _, err := rootLam.AddPermission(ctx, &awslambda.AddPermissionInput{FunctionName: aws.String("fn"), StatementId: aws.String("nocarol"),
		Action: aws.String("lambda:GetFunction"), Principal: aws.String(userARN("carol"))}); err != nil {
		t.Fatal(err)
	}
	// AddPermission only writes Allows; prove the policy is read on a
	// qualified reference by giving a user nothing but the resource policy.
	dan := awslambda.NewFromConfig(userConfig(t, root, "dan", noPolicy), func(o *awslambda.Options) { o.BaseEndpoint = aws.String(endpoint) })
	if _, err := rootLam.AddPermission(ctx, &awslambda.AddPermissionInput{FunctionName: aws.String("fn"), StatementId: aws.String("dan"),
		Action: aws.String("lambda:GetFunction"), Principal: aws.String(userARN("dan"))}); err != nil {
		t.Fatal(err)
	}
	if _, err := dan.GetFunction(ctx, &awslambda.GetFunctionInput{FunctionName: aws.String("fn")}); err != nil {
		t.Fatalf("the function policy admits dan on the bare name: %v", err)
	}
	_, err = dan.GetFunction(ctx, &awslambda.GetFunctionInput{FunctionName: aws.String("fn:$LATEST")})
	// The statement names the unqualified ARN; a qualified reference is a
	// different resource, and nothing else admits dan.
	wantDenied(t, err, "a qualified reference is its own resource", "lambda:GetFunction")
}
