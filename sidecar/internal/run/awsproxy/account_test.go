// Copyright 2026 The Kstack Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package awsproxy

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kstackhq/kstack/sidecar/internal/lib/testutil"
	"github.com/kstackhq/kstack/sidecar/internal/run/permissions"
)

const account = "123456789012"

// classified is a request of method to path on service in region, with body
// of contentType, read as the handler reads it.
func classified(service, region, method, path, contentType, body string) *request {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	if contentType != "" {
		r.Header.Set("Content-Type", contentType)
	}
	req := &request{r: r, cred: credential{service: service, region: region}, row: services[service], body: []byte(body)}
	req.op = classify(service, r, req.body)
	return req
}

func TestAScopeIsLowerCase(t *testing.T) {
	req := classified("ec2", "EU-West-1", http.MethodPost, "/", formType, "Action=StartInstances")
	assert.Equal(t, account+"/eu-west-1", actionScope(req, testCreds))
}

const formType = "application/x-www-form-urlencoded"

// A request naming another account, or whose account Kstack cannot trust, has
// no scope, so it asks each time.
func TestAnAliasedOrForeignScopeIsEmpty(t *testing.T) {
	kms := classified("s3", "eu-west-1", http.MethodPut, "/bucket/key", "", "")
	kms.r.Header.Set("X-Amz-Server-Side-Encryption-Aws-Kms-Key-Id", "arn:aws:kms:eu-west-1:210987654321:key/k")
	bucket := classified("s3", "eu-west-1", http.MethodGet, "/theirs/key", "", "")
	bucket.foreign = true
	for name, req := range map[string]*request{
		"an ARN in a JSON body": classified("logs", "eu-west-1", http.MethodPost, "/", "application/x-amz-json-1.1",
			`{"destinationArn":"arn:aws:logs:eu-west-1:210987654321:destination:d"}`),
		"an id as a JSON key": classified("logs", "eu-west-1", http.MethodPost, "/", "application/x-amz-json-1.1", `{"210987654321":1}`),
		"an id as a JSON number": classified("logs", "eu-west-1", http.MethodPost, "/", "application/x-amz-json-1.1",
			`{"accountId":210987654321}`),
		"an escaped id": classified("logs", "eu-west-1", http.MethodPost, "/", "application/x-amz-json-1.1", `{"a":"210987654321"}`),
		"an id in a form value": classified("ec2", "eu-west-1", http.MethodPost, "/", formType,
			"Action=ModifySnapshotAttribute&CreateVolumePermission.Add.1.UserId=210987654321"),
		"an id in a query value":       classified("eks", "eu-west-1", http.MethodPost, "/clusters?role=arn:aws:iam::210987654321:role/r", "", ""),
		"a body that is neither":       classified("route53", "us-east-1", http.MethodPost, "/2013-04-01/hostedzone/Z/rrset", "text/xml", "<x/>"),
		"a JSON body that is not JSON": classified("eks", "eu-west-1", http.MethodPost, "/clusters", "application/json", `{"a":`),
		"a form that does not parse":   classified("ec2", "eu-west-1", http.MethodPost, "/", formType, "Action=StartInstances&a=%zz"),
		"a KMS key of another account": kms,
		"another account's bucket":     bucket,
		"another account's registry":   classified("ecr", "eu-west-1", http.MethodPost, "/", "application/x-amz-json-1.1", `{"registryId":"x"}`),
	} {
		if req.r.Header.Get("Content-Type") == "application/x-amz-json-1.1" {
			req.r.Header.Set("X-Amz-Target", "X.PutThing")
			req.op = classify(req.cred.service, req.r, req.body)
		}
		assert.Empty(t, actionScope(req, testCreds), name)
	}
	noAccount := testCreds
	noAccount.AccountID = ""
	start := classified("ec2", "eu-west-1", http.MethodPost, "/", formType, "Action=StartInstances")
	assert.Empty(t, actionScope(start, noAccount), "a login with no account id")
	for _, id := range []string{"12345678901", "1234567890123", "arn:aws:iam::123456789012:root", "123456789012/x"} {
		assert.Empty(t, actionScope(start, aws.Credentials{AccountID: id}), id)
	}

	// What names the login's own account, or no account, keeps its scope.
	for name, req := range map[string]*request{
		"its own id": classified("ec2", "eu-west-1", http.MethodPost, "/", formType,
			"Action=CreateTags&ResourceId.1=arn:aws:ec2:eu-west-1:123456789012:instance/i-1"),
		"no body":                     classified("eks", "eu-west-1", http.MethodDelete, "/clusters/c", "", ""),
		"a JSON body":                 classified("eks", "eu-west-1", http.MethodPost, "/clusters", "application/json", `{"name":"c","version":"1.31"}`),
		"a JSON body of no string":    classified("eks", "eu-west-1", http.MethodPost, "/clusters", "application/json", `{"a":[true,null,1]}`),
		"a shorter number":            classified("ec2", "eu-west-1", http.MethodPost, "/", formType, "Action=StartInstances&Count=21098765432"),
		"an object's payload, unread": classified("s3", "eu-west-1", http.MethodPut, "/bucket/key", "text/plain", "account 210987654321"),
	} {
		assert.Equal(t, account+"/eu-west-1", actionScope(req, testCreds), name)
	}
}

// A global service has one host, so one scope whatever region signed it.
func TestAGlobalAWSServiceHasOneScope(t *testing.T) {
	for _, region := range []string{"us-east-1", "eu-west-1", "ap-southeast-2"} {
		f := newFixture(t)
		f.asker.approve = false
		r := sandboxRequest(http.MethodDelete, "/2013-04-01/hostedzone/Z1", f.keyID(), region, "route53", "")
		f.serve(r)
		asked := testutil.Recv(t, f.asker.asked, "the request")
		assert.Equal(t, account+"/global", asked.Action.Scope, region)
	}
}

// A read is classified as it was: it reaches no decision, so it runs unasked
// and unrecorded under every mode, a login with no account id included.
func TestAReadIsClassifiedAsToday(t *testing.T) {
	f := newFixture(t)
	f.id.noAccount = true
	f.setPolicy(permissions.ModeReadOnly, permissions.Rule{Effect: permissions.Deny, AccessLevel: permissions.Write,
		Principal: "*", Target: "*", Scope: "*", Service: "*", Operation: "*", Resource: "*"})
	for _, r := range []*http.Request{f.ec2(), f.query("iam", "ListRoles"), f.request(http.MethodGet, "/2013-04-01/hostedzone", "route53", "")} {
		w := f.serve(r)
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())
		f.sent()
	}
	assert.Empty(t, f.asker.asked)
	assert.Empty(t, f.asker.recorded)
}

// allowRule is an allow of level in the login's account.
func allowRule(level permissions.AccessLevel) permissions.Rule {
	return permissions.Rule{ID: "a", Effect: permissions.Allow, AccessLevel: level, Principal: "*", Target: "aws",
		Scope: account + "/**", Service: "*", Operation: "*", Resource: "*"}
}

func TestARuleAndAModeDecideAnAWSAction(t *testing.T) {
	start := func(f *fixture) *http.Request { return f.query("ec2", "StartInstances&InstanceId.1=i-1") }
	t.Run("auto forwards a write unasked and records it", func(t *testing.T) {
		f := newFixture(t)
		f.setPolicy(permissions.ModeAuto)
		require.Equal(t, http.StatusOK, f.serve(start(f)).Code)
		rec := testutil.Recv(t, f.asker.recorded, "the record")
		assert.Equal(t, permissions.Allowed, rec.d)
		assert.Equal(t, "auto mode", rec.reason)
		assert.NoError(t, f.sent().signedBy)
		assert.Empty(t, f.asker.asked)
	})
	t.Run("read-only denies", func(t *testing.T) {
		f := newFixture(t)
		f.setPolicy(permissions.ModeReadOnly)
		w := f.serve(start(f))
		assert.Equal(t, http.StatusForbidden, w.Code)
		assert.Contains(t, w.Body.String(), "this account is read-only")
		assert.Equal(t, permissions.Denied, testutil.Recv(t, f.asker.recorded, "the record").d)
		f.none()
	})
	t.Run("an allow rule forwards", func(t *testing.T) {
		f := newFixture(t)
		f.setPolicy(permissions.ModeAsk, allowRule(permissions.Write))
		require.Equal(t, http.StatusOK, f.serve(start(f)).Code)
		assert.Equal(t, "a rule allows it: Allow writes anywhere in AWS 123456789012", testutil.Recv(t, f.asker.recorded, "the record").reason)
		f.sent()
	})
	t.Run("a deny rule refuses", func(t *testing.T) {
		f := newFixture(t)
		deny := allowRule(permissions.Write)
		deny.Effect = permissions.Deny
		f.setPolicy(permissions.ModeAuto, deny)
		assert.Equal(t, http.StatusForbidden, f.serve(start(f)).Code)
		f.none()
	})
	t.Run("a command answer allows the command's next", func(t *testing.T) {
		f := newFixture(t)
		f.asker.duration = permissions.DurationCommand
		require.Equal(t, http.StatusOK, f.serve(start(f)).Code)
		asked := testutil.Recv(t, f.asker.asked, "the first")
		assert.True(t, asked.Grantable)
		assert.Equal(t, "Allow writes in AWS 123456789012 / eu-west-1", asked.ChatRule)
		f.sent()
		require.Equal(t, http.StatusOK, f.serve(start(f)).Code)
		assert.Empty(t, f.asker.asked, "the next runs unasked")
		assert.Equal(t, permissions.Allowed, testutil.Recv(t, f.asker.recorded, "the record").d)
		f.sent()
	})
	t.Run("a data read in the account runs under auto", func(t *testing.T) {
		f := newFixture(t)
		f.owns("mine")
		f.setPolicy(permissions.ModeAuto)
		require.Equal(t, http.StatusOK, f.serve(f.request(http.MethodGet, "/mine/key", "s3", "")).Code)
		rec := testutil.Recv(t, f.asker.recorded, "the record")
		assert.Equal(t, permissions.Allowed, rec.d)
		assert.Equal(t, permissions.DataRead, rec.req.Action.AccessLevel)
		f.sent()
	})
	t.Run("a data read naming another account asks once", func(t *testing.T) {
		f := newFixture(t)
		f.owns("mine")
		f.setPolicy(permissions.ModeAuto, allowRule(permissions.DataRead))
		f.asker.approve = false
		for _, r := range []*http.Request{
			f.request(http.MethodGet, "/theirs/key", "s3", ""),
			f.json("logs", "Logs_20140328.GetQueryResults", `{"queryId":"arn:aws:logs:eu-west-1:210987654321:q"}`),
		} {
			assert.Equal(t, http.StatusForbidden, f.serve(r).Code)
			asked := testutil.Recv(t, f.asker.asked, "the request")
			assert.False(t, asked.Grantable)
			assert.Empty(t, asked.Action.Scope)
		}
		f.none()
	})
}

// The action decided, asked and recorded holds every field the classifier
// read, and the credentials are resolved once for the decision and the
// forward.
func TestNoProxyDropsAFieldBeforeTheDecision(t *testing.T) {
	f := newFixture(t)
	f.owns("bucket")
	require.Equal(t, http.StatusOK, f.serve(f.request(http.MethodPut, "/bucket/a/b", "s3", "")).Code)
	asked := testutil.Recv(t, f.asker.asked, "the request")
	assert.Equal(t, permissions.Action{
		Principal: permissions.Chat, Target: permissions.AWS, Scope: account + "/eu-west-1", AccountName: account,
		Service: "s3", Operation: "PUT", Resource: "object", Name: "bucket/a/b", AccessLevel: permissions.Write,
		Summary: "AWS: call PUT s3 /bucket/a/b in eu-west-1",
	}, asked.Action)
	f.sent()
	assert.Equal(t, 2, f.id.calls, "one resolve for the decision and one for the forward")
}

func TestAnUnnamedOperationAsksOnceAndIsNotGranted(t *testing.T) {
	f := newFixture(t)
	f.setPolicy(permissions.ModeAuto, allowRule(permissions.Write))
	f.asker.approve, f.asker.duration = true, permissions.DurationCommand
	for range 2 {
		require.Equal(t, http.StatusOK, f.serve(f.query("ec2", "RunInstances")).Code)
		asked := testutil.Recv(t, f.asker.asked, "the request")
		assert.True(t, asked.Action.Once)
		assert.False(t, asked.Grantable)
		f.sent()
	}
}

// The forward signs with credentials read once the request is allowed, and a
// login that became another account's meanwhile sends nothing.
func TestALoginThatChangedAccountSendsNothing(t *testing.T) {
	f := newFixture(t)
	f.setPolicy(permissions.ModeAuto)
	f.id.laterAccount = "999999999999"

	w := f.serve(f.query("ec2", "StartInstances&InstanceId.1=i-1"))

	assert.Equal(t, http.StatusForbidden, w.Code)
	assert.Contains(t, w.Body.String(), "the AWS login changed")
	f.none()
}

// A login that is gone by the time the request is allowed sends nothing.
func TestALoginGoneAtTheForwardSendsNothing(t *testing.T) {
	f := newFixture(t)
	f.setPolicy(permissions.ModeAuto)
	f.id.laterErr = errors.New("expired")

	w := f.serve(f.query("ec2", "StartInstances&InstanceId.1=i-1"))

	assert.Equal(t, http.StatusForbidden, w.Code)
	assert.Contains(t, w.Body.String(), "no AWS login was found")
	f.none()
}
