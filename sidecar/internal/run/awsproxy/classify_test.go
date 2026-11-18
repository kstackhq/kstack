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
	"cmp"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/kstackhq/kstack/sidecar/internal/run/permissions"
)

// call is one request for the classifier: a query call's form, a JSON call's
// target and body, or a REST method and path.
type call struct {
	service, method, path string
	form, target, body    string
}

func (c call) classify() operation {
	method, body := c.method, c.body
	if method == "" {
		method = http.MethodPost
	}
	r := httptest.NewRequest(method, cmp.Or(c.path, "/"), nil)
	if c.form != "" {
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded; charset=utf-8")
		body = c.form
	}
	if c.target != "" {
		r.Header.Set("X-Amz-Target", c.target)
	}
	return classify(c.service, r, []byte(body))
}

const (
	read      = permissions.Class(0)
	change    = permissions.UpstreamWrite
	destroy   = permissions.Destructive
	secret    = permissions.SecretRead
	refusedOp = permissions.Class(-1)
)

// Every operation this spec names, and each prefix, lands in its class.
func TestEveryRowsOperationsAreClassed(t *testing.T) {
	for want, calls := range map[permissions.Class][]call{
		read: {
			{service: "sts", form: "Action=GetCallerIdentity&Version=2011-06-15"},
			{service: "ec2", form: "Action=DescribeInstances"},
			{service: "ec2", form: "Action=DescribeInstanceAttribute&Attribute=instanceType"},
			{service: "iam", form: "Action=ListRoles"},
			{service: "iam", form: "Action=GenerateCredentialReport"},
			{service: "iam", form: "Action=SimulatePrincipalPolicy"},
			{service: "ec2", method: "GET", path: "/?Action=DescribeRegions"},
			{service: "logs", target: "Logs_20140328.FilterLogEvents", body: `{"logGroupName":"g"}`},
			{service: "logs", target: "Logs_20140328.StartQuery", body: `{"queryString":"fields @message"}`},
			{service: "monitoring", target: "GraniteServiceVersion20100801.GetMetricData", body: `{}`},
			{service: "monitoring", form: "Action=ListMetrics"},
			{service: "ecr", target: "AmazonEC2ContainerRegistry_V20150921.BatchGetImage", body: `{"repositoryName":"r"}`},
			{service: "cloudformation", form: "Action=ValidateTemplate&TemplateBody=x"},
			{service: "cloudformation", form: "Action=ListStacks"},
			{service: "s3", method: "GET", path: "/"},
			{service: "s3", method: "GET", path: "/bucket?list-type=2"},
			{service: "s3", method: "HEAD", path: "/bucket/key"},
			{service: "s3", method: "GET", path: "/bucket/key?tagging"},
			{service: "s3", method: "GET", path: "/bucket/key?uploadId=u"},
			{service: "eks", method: "GET", path: "/clusters"},
			{service: "route53", method: "GET", path: "/2013-04-01/hostedzone"},
		},
		change: {
			{service: "ec2", form: "Action=StopInstances&InstanceId.1=i-1"},
			{service: "ec2", method: "GET", path: "/"},
			{service: "ec2", form: "Version=2016-11-15"},
			{service: "ec2", form: "Action=DescribeInstances&x=%zz"},
			{service: "logs", body: `{}`},
			{service: "ecr", target: "AmazonEC2ContainerRegistry_V20150921.BatchGetImage", body: `{"registryId":"999999999999","repositoryName":"r"}`},
			{service: "ecr", target: "AmazonEC2ContainerRegistry_V20150921.DescribeImages", body: `{"registryId":"9"}`},
			{service: "cloudformation", form: "Action=ValidateTemplate&TemplateURL=https://b.s3.amazonaws.com/t"},
			{service: "cloudformation", form: "Action=EstimateTemplateCost&TemplateURL=https://b.s3.amazonaws.com/t"},
			{service: "s3", method: "PUT", path: "/bucket/key"},
			{service: "eks", method: "POST", path: "/clusters"},
		},
		destroy: {
			{service: "ec2", form: "Action=TerminateInstances"},
			{service: "ec2", form: "Action=DeleteVpc"},
			{service: "logs", target: "Logs_20140328.DeleteLogGroup", body: `{}`},
			{service: "iam", form: "Action=AttachRolePolicy"},
			{service: "iam", form: "Action=PutUserPolicy"},
			{service: "iam", form: "Action=UpdateAssumeRolePolicy"},
			{service: "iam", form: "Action=CreateLoginProfile"},
			{service: "s3", method: "DELETE", path: "/bucket/key"},
			{service: "s3", method: "POST", path: "/bucket?delete"},
			{service: "eks", method: "DELETE", path: "/clusters/c"},
		},
		secret: {
			{service: "s3", method: "GET", path: "/bucket/key"},
			{service: "s3", method: "GET", path: "/bucket/a/b?versionId=v"},
			{service: "s3", method: "POST", path: "/bucket/key?select&select-type=2"},
			{service: "ec2", form: "Action=DescribeInstanceAttribute&Attribute=userData"},
			{service: "ec2", form: "Action=DescribeInstanceAttribute"},
			{service: "ec2", form: "Action=DescribeInstanceAttribute&Attribute=kernel&Attribute=userData"},
			{service: "ec2", form: "Action=DescribeLaunchTemplateVersions"},
			{service: "ec2", form: "Action=GetConsoleOutput"},
			{service: "ec2", form: "Action=DescribeVpnConnections"},
			{service: "ec2", form: "Action=GetVpnConnectionDeviceSampleConfiguration"},
			{service: "autoscaling", form: "Action=DescribeLaunchConfigurations"},
			{service: "cloudformation", form: "Action=DescribeStacks"},
			{service: "cloudformation", form: "Action=GetTemplate"},
			{service: "logs", target: "Logs_20140328.GetLogEvents", body: `{"unmask":true}`},
			{service: "logs", target: "Logs_20140328.FilterLogEvents", body: `{"unmask":false,"unmask":true}`},
			{service: "logs", target: "Logs_20140328.StartQuery", body: `{"queryString":"fields unmask(@message)"}`},
			{service: "logs", target: "Logs_20140328.StartQuery", body: `{"queryString":"fields UNMASK (@message)"}`},
			{service: "logs", target: "Logs_20140328.StartQuery", body: `{"queryLanguage":"SQL","queryString":"SELECT Unmask(x)"}`},
			{service: "logs", target: "Logs_20140328.GetLogRecord", body: `not json`},
			{service: "logs", target: "Logs_20140328.GetQueryResults", body: `{"queryId":"q"}`},
		},
		refusedOp: {
			{service: "sts", form: "Action=AssumeRole"},
			{service: "sts", form: "Action=GetSessionToken"},
			{service: "ec2", form: "Action=CreateKeyPair"},
			{service: "ec2", form: "Action=GetPasswordData"},
			{service: "iam", form: "Action=CreateAccessKey"},
			{service: "iam", form: "Action=CreateVirtualMFADevice"},
			{service: "ecr", target: "AmazonEC2ContainerRegistry_V20150921.GetAuthorizationToken", body: `{}`},
			{service: "ecr", target: "AmazonEC2ContainerRegistry_V20150921.GetDownloadUrlForLayer", body: `{}`},
			{service: "s3", method: "GET", path: "/bucket?session"},
		},
	} {
		for _, c := range calls {
			op := c.classify()
			got := op.class
			if op.refusal != "" {
				got = refusedOp
			}
			assert.Equal(t, want, got, "%+v", c)
		}
	}
}

// A non-REST operation is read only where its row's protocol names it, and a
// request naming it elsewhere or twice is refused.
func TestAnOperationIsReadOnlyWhereItsRowNamesIt(t *testing.T) {
	for _, c := range []call{
		{service: "monitoring", form: "Action=ListMetrics"},
		{service: "monitoring", target: "GraniteServiceVersion20100801.ListMetrics", body: `{}`},
	} {
		op := c.classify()
		assert.Empty(t, op.refusal, "%+v", c)
		assert.Equal(t, "ListMetrics", op.name, "%+v", c)
	}
	for _, c := range []call{
		{service: "ec2", form: "Action=TerminateInstances", target: "AmazonEC2.DescribeInstances"},
		{service: "ec2", target: "AmazonEC2.DescribeInstances", body: `{}`},
		{service: "logs", path: "/?Action=DescribeLogGroups", target: "Logs_20140328.DescribeLogGroups", body: `{}`},
		{service: "logs", path: "/?Action=DescribeLogGroups"},
		{service: "monitoring", form: "Action=PutMetricAlarm", target: "GraniteServiceVersion20100801.ListMetrics"},
		{service: "ec2", form: "Action=DescribeInstances&Action=TerminateInstances"},
		{service: "sts", path: "/?Action=AssumeRole", form: "Action=AssumeRole"},
		{service: "ec2", path: "/?Action=DescribeInstances", form: "Action=TerminateInstances"},
	} {
		assert.Contains(t, c.classify().refusal, "names its operation where", "%+v", c)
	}
}

// A form body is read for its Action however leniently its Content-Type is
// written, as runproxy reads it to show it, so the Action AWS runs is read.
func TestAFormIsReadWhateverItsTypesParameters(t *testing.T) {
	r := httptest.NewRequest(http.MethodPost, "/?Action=DescribeInstances", nil)
	r.Header.Set("Content-Type", "Application/X-WWW-Form-Urlencoded; a=1; a=2")
	op := classify("ec2", r, []byte("Action=TerminateInstances"))
	assert.Contains(t, op.refusal, "names its operation where")
}

// A query or JSON operation is named for the summary; a REST one is not.
func TestTheOperationIsNamed(t *testing.T) {
	assert.Equal(t, "DescribeInstances", call{service: "ec2", form: "Action=DescribeInstances"}.classify().name)
	assert.Equal(t, "GetMetricData", call{service: "monitoring", target: "GraniteServiceVersion20100801.GetMetricData", body: "{}"}.classify().name)
	assert.Empty(t, call{service: "s3", method: "GET", path: "/b"}.classify().name)
}

// An S3 request's bucket is its path's first segment.
func TestAnS3RequestNamesItsBucket(t *testing.T) {
	for path, want := range map[string]string{"/": "", "/b": "b", "/b/": "b", "/b/k/x": "b", "/b?acl": "b"} {
		assert.Equal(t, want, call{service: "s3", method: "GET", path: path}.classify().bucket, path)
	}
	assert.Empty(t, call{service: "ec2", form: "Action=DescribeInstances"}.classify().bucket)
}
