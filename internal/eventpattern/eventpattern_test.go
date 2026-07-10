package eventpattern

import "testing"

// event is the sample EC2 state-change event AWS's pattern docs use.
const event = `{
	"version": "0",
	"id": "6a7e8feb-b491-4cf7-a9f1-bf3703467718",
	"detail-type": "EC2 Instance State-change Notification",
	"source": "aws.ec2",
	"account": "111122223333",
	"time": "2017-12-22T18:43:48Z",
	"region": "us-west-1",
	"resources": ["arn:aws:ec2:us-west-1:123456789012:instance/i-1234567890abcdef0"],
	"detail": {
		"instance-id": "i-1234567890abcdef0",
		"state": "terminated",
		"cpu": 75.5,
		"ip": "10.0.0.42",
		"enabled": true,
		"note": null
	}
}`

func match(t *testing.T, pattern string) bool {
	t.Helper()
	p, err := Parse([]byte(pattern))
	if err != nil {
		t.Fatalf("Parse(%s): %v", pattern, err)
	}
	ok, err := p.Match([]byte(event))
	if err != nil {
		t.Fatal(err)
	}
	return ok
}

func TestPatternMatching(t *testing.T) {
	cases := []struct {
		name    string
		pattern string
		want    bool
	}{
		{"exact source", `{"source": ["aws.ec2"]}`, true},
		{"exact miss", `{"source": ["aws.s3"]}`, false},
		{"or within field", `{"source": ["aws.s3", "aws.ec2"]}`, true},
		{"and across fields", `{"source": ["aws.ec2"], "detail-type": ["EC2 Instance State-change Notification"]}`, true},
		{"and across fields miss", `{"source": ["aws.ec2"], "detail-type": ["Nope"]}`, false},
		{"nested detail", `{"detail": {"state": ["terminated"]}}`, true},
		{"nested miss", `{"detail": {"state": ["running"]}}`, false},
		{"event array any-element", `{"resources": [{"prefix": "arn:aws:ec2:us-west-1"}]}`, true},
		{"prefix", `{"region": [{"prefix": "us-"}]}`, true},
		{"suffix", `{"detail": {"instance-id": [{"suffix": "def0"}]}}`, true},
		{"equals-ignore-case", `{"detail-type": [{"equals-ignore-case": "ec2 instance state-change notification"}]}`, true},
		{"wildcard", `{"source": [{"wildcard": "aws.*"}]}`, true},
		{"wildcard middle", `{"detail": {"instance-id": [{"wildcard": "i-*abcdef0"}]}}`, true},
		{"wildcard miss", `{"source": [{"wildcard": "gcp.*"}]}`, false},
		{"anything-but value", `{"detail": {"state": [{"anything-but": "running"}]}}`, true},
		{"anything-but hit", `{"detail": {"state": [{"anything-but": "terminated"}]}}`, false},
		{"anything-but list", `{"detail": {"state": [{"anything-but": ["pending", "running"]}]}}`, true},
		{"anything-but prefix", `{"detail": {"state": [{"anything-but": {"prefix": "term"}}]}}`, false},
		{"numeric range", `{"detail": {"cpu": [{"numeric": [">", 50, "<=", 80]}]}}`, true},
		{"numeric miss", `{"detail": {"cpu": [{"numeric": ["<", 50]}]}}`, false},
		{"numeric equals", `{"detail": {"cpu": [{"numeric": ["=", 75.5]}]}}`, true},
		{"exists true", `{"detail": {"instance-id": [{"exists": true}]}}`, true},
		{"exists false on absent", `{"detail": {"missing-field": [{"exists": false}]}}`, true},
		{"exists true on absent", `{"detail": {"missing-field": [{"exists": true}]}}`, false},
		{"cidr hit", `{"detail": {"ip": [{"cidr": "10.0.0.0/24"}]}}`, true},
		{"cidr miss", `{"detail": {"ip": [{"cidr": "192.168.0.0/16"}]}}`, false},
		{"bool", `{"detail": {"enabled": [true]}}`, true},
		{"null", `{"detail": {"note": [null]}}`, true},
		{"or across fields", `{"$or": [{"source": ["aws.s3"]}, {"region": ["us-west-1"]}]}`, true},
		{"or across fields miss", `{"$or": [{"source": ["aws.s3"]}, {"region": ["eu-west-1"]}]}`, false},
		{"mixed exact and operator", `{"detail": {"state": ["stopped", {"prefix": "term"}]}}`, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := match(t, tc.pattern); got != tc.want {
				t.Errorf("pattern %s matched=%v, want %v", tc.pattern, got, tc.want)
			}
		})
	}
}

func TestParseErrors(t *testing.T) {
	for name, pattern := range map[string]string{
		"not an object": `[1,2]`,
		"empty pattern": `{}`,
		"empty leaf":    `{"source": []}`,
		"unknown op":    `{"source": [{"regex": "a.*"}]}`,
		"bad numeric":   `{"n": [{"numeric": ["~", 5]}]}`,
		"bad cidr":      `{"ip": [{"cidr": "not-a-cidr"}]}`,
		"multi-key op":  `{"source": [{"prefix": "a", "suffix": "b"}]}`,
		"empty or":      `{"$or": []}`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := Parse([]byte(pattern)); err == nil {
				t.Errorf("accepted %s", pattern)
			}
		})
	}
}

// FuzzPattern asserts parse+match never panic.
func FuzzPattern(f *testing.F) {
	f.Add(`{"source": ["aws.ec2"]}`, event)
	f.Add(`{"detail": {"cpu": [{"numeric": [">", 50]}]}}`, `{"detail": {"cpu": 60}}`)
	f.Add(`{"$or": [{"a": [1]}, {"b": [{"exists": false}]}]}`, `{"a": 1}`)
	f.Add(`{`, `{`)
	f.Fuzz(func(t *testing.T, pattern, evt string) {
		p, err := Parse([]byte(pattern))
		if err != nil {
			return
		}
		p.Match([]byte(evt))
	})
}
