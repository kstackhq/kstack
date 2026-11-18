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

package azureproxy

import (
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kstackhq/kstack/sidecar/internal/run/permissions"
)

const (
	rg      = "/subscriptions/" + sub + "/resourceGroups/rg"
	compute = rg + "/providers/Microsoft.Compute/virtualMachines/vm-1"
	aks     = rg + "/providers/Microsoft.ContainerService/managedClusters/aks-1"
	site    = rg + "/providers/Microsoft.Web/sites/shop"
)

// The expected reading of a request: a read, a class that asks, or a refusal.
const (
	isRead    = "read"
	isRefused = "refused"
	isClass4  = "class 4"
	isClass5  = "class 5"
	isClass6  = "class 6"
)

// readingOf classifies method and target, a path with its query, as the
// handler does: the path policy first.
func readingOf(t *testing.T, method, target string) string {
	t.Helper()
	p, query, _ := strings.Cut(target, "?")
	a, ok := parsePath(method, p)
	if !ok {
		return isRefused
	}
	q, err := url.ParseQuery(query)
	require.NoError(t, err)
	v := classify(method, a, q)
	switch {
	case v.refusal != "":
		return isRefused
	case v.class == 0:
		return isRead
	}
	return map[permissions.Class]string{
		permissions.UpstreamWrite: isClass4, permissions.Destructive: isClass5, permissions.SecretRead: isClass6,
	}[v.class]
}

// One table over the path policy and the classifier's rows. Each request is
// also tried in upper and lower case, which Resource Manager reads alike.
func TestEveryRequestIsClassified(t *testing.T) {
	for _, c := range []struct{ method, target, want string }{
		// The path policy.
		{"GET", "/batch", isRefused},
		{"POST", "/batch", isRefused},
		{"GET", "/subscriptions/" + sub + "/a/b/c", isRefused},
		{"OPTIONS", compute, isRefused},
		{"TRACE", compute, isRefused},
		// Row 1: AKS run-command.
		{"POST", aks + "/runCommand", isRefused},
		{"GET", aks + "/commandResults/c1", isRefused},
		// Row 2: AKS access profiles.
		{"GET", aks + "/accessProfiles/clusterAdmin", isRefused},
		{"POST", aks + "/accessProfiles/clusterUser/listCredential", isRefused},
		// Row 3: secret metadata.
		{"POST", site + "/config/appsettings/list", isClass6},
		{"POST", site + "/slots/staging/config/appsettings/list", isClass6},
		{"POST", site + "/config/connectionstrings/list", isClass6},
		{"GET", compute + "?$expand=userData", isClass6},
		{"GET", compute + "?$Expand=userData", isClass6},
		{"GET", compute + "?$EXPAND=UserData", isClass6},
		{"GET", rg + "/providers/Microsoft.ApiManagement/service/apim/subscriptions", isClass6},
		{"GET", rg + "/providers/Microsoft.ApiManagement/service/apim/subscriptions/s1", isClass6},
		{"GET", rg + "/providers/Microsoft.ApiManagement/service/apim/users/u/subscriptions", isClass6},
		{"GET", rg + "/providers/Microsoft.ApiManagement/service/apim", isRead},
		{"GET", site + "/hybridConnectionNamespaces/ns/relays/r", isClass6},
		{"GET", site + "/slots/staging/hybridConnectionNamespaces/ns/relays/r", isClass6},
		{"GET", rg + "/providers/Microsoft.Web/serverfarms/plan/hybridConnectionNamespaces/ns/relays/r", isClass6},
		{"GET", rg + "/providers/Microsoft.Compute/virtualMachineScaleSets/ss?$expand=instanceView,userData", isClass6},
		{"GET", rg + "/providers/Microsoft.Resources/deployments", isClass6},
		{"GET", rg + "/providers/Microsoft.Resources/deployments/d1", isClass6},
		{"GET", "/subscriptions/" + sub + "/providers/Microsoft.Resources/deployments/d1", isClass6},
		{"POST", rg + "/providers/Microsoft.Resources/deployments/d1/exportTemplate", isClass6},
		{"GET", rg + "/providers/Microsoft.Automation/automationAccounts/a/variables", isClass6},
		{"GET", rg + "/providers/Microsoft.Automation/automationAccounts/a/variables/v", isClass6},
		{"GET", "/subscriptions/" + sub + "/providers/Microsoft.App/containerApps", isClass6},
		{"GET", rg + "/providers/Microsoft.App/containerApps/web", isClass6},
		{"GET", rg + "/providers/Microsoft.ContainerInstance/containerGroups", isClass6},
		{"GET", rg + "/providers/Microsoft.ContainerInstance/containerGroups/cg", isClass6},
		{"GET", rg + "/providers/Microsoft.App/containerApps/web/revisions", isClass6},
		{"GET", rg + "/providers/Microsoft.App/containerApps/web/revisions/web--r1", isClass6},
		{"GET", rg + "/providers/Microsoft.App/jobs", isClass6},
		{"GET", rg + "/providers/Microsoft.App/jobs/nightly", isClass6},
		{"GET", rg + "/providers/Microsoft.App/jobs/nightly/executions", isClass6},
		{"GET", rg + "/providers/Microsoft.App/jobs/nightly/executions/e1", isClass6},
		// Row 4: an action that answers a credential.
		{"POST", rg + "/providers/Microsoft.Storage/storageAccounts/sa/listKeys", isRefused},
		{"POST", rg + "/providers/Microsoft.Storage/storageAccounts/sa/regenerateKey", isRefused},
		{"POST", rg + "/providers/Microsoft.Storage/storageAccounts/sa/listAccountSas", isRefused},
		{"POST", aks + "/listClusterAdminCredential", isRefused},
		{"POST", rg + "/providers/Microsoft.ContainerRegistry/registries/r/generateCredentials", isRefused},
		{"POST", rg + "/providers/Microsoft.Compute/sshPublicKeys/k/generateKeyPair", isRefused},
		{"POST", rg + "/providers/Microsoft.Sql/servers/s/resetPassword", isRefused},
		{"POST", rg + "/providers/Microsoft.Web/sites/shop/functions/f/listKeys", isRefused},
		{"POST", site + "/publishxml", isRefused},
		{"POST", site + "/slots/staging/publishxml", isRefused},
		{"GET", site + "/publishxml", isRefused},
		{"GET", rg + "/providers/Microsoft.DocumentDB/databaseAccounts/db/readonlykeys", isRefused},
		{"POST", rg + "/providers/Microsoft.DocumentDB/databaseAccounts/db/readonlykeys", isRefused},
		{"GET", rg + "/providers/Microsoft.DocumentDB/databaseAccounts/db", isRead},
		// Row 5: any other list or get action.
		{"POST", "/subscriptions/" + sub + "/providers/Microsoft.Compute/locations/westus/listUsages", isRefused},
		{"POST", site + "/config/publishingcredentials/list", isRefused},
		{"POST", rg + "/providers/Microsoft.Logic/workflows/w/triggers/t/listCallbackUrl", isRefused},
		{"POST", rg + "/providers/Microsoft.Network/networkWatchers/nw/getTopology", isRefused},
		// Row 6: reads.
		{"GET", "/subscriptions", isRead},
		{"GET", "/tenants", isRead},
		{"GET", "/subscriptions/" + sub + "/resourcegroups", isRead},
		{"HEAD", rg, isRead},
		{"GET", "/subscriptions/" + sub + "/providers/Microsoft.ContainerService/managedClusters", isRead},
		{"GET", compute, isRead},
		{"GET", compute + "?$expand=instanceView", isRead},
		{"GET", rg + "/providers/Microsoft.KeyVault/vaults/kv/secrets/s", isRead},
		{"GET", rg + "/providers/Microsoft.Resources/deployments/d1/operationStatuses/op", isRead},
		{"GET", "/subscriptions/" + sub + "/operationresults/abc", isRead},
		{"GET", rg + "/operationresults/abc", isRead},
		{"GET", "/subscriptions/" + sub + "/providers/Microsoft.Compute/locations/westus/operations/op", isRead},
		{"GET", "/subscriptions/" + sub + "/providers/Microsoft.Compute/locations/westus/operationResults/op", isRead},
		{"GET", "/subscriptions/" + sub + "/providers/Microsoft.Network/locations/westus/operationStatuses/op", isRead},
		{"GET", compute + "/operationResults/op", isRead},
		{"GET", rg + "/providers/Microsoft.App/containerApps/web/operationResults/op", isClass6},
		{"GET", "/subscriptions/" + sub + "/providers/Microsoft.App/locations/westus/containerappOperationResults/op", isClass6},
		{"GET", site + "/operationresults/op", isClass6},
		{"GET", rg + "/providers/Microsoft.ContainerInstance/locations/westus/operationResults/op", isClass6},
		{"GET", rg + "/providers/Microsoft.App/containerApps/web/operationStatuses/op", isRead},
		{"GET", compute + "/operationStatuses/op", isRead},
		{"DELETE", "/subscriptions/" + sub + "/operationresults/abc", isRefused},
		{"GET", "/subscriptions/" + sub + "/resourcegroups/rg/providers/Microsoft.Compute/virtualMachines/vm-1", isRead},
		// Row 7: class 5.
		{"DELETE", rg, isClass5},
		{"DELETE", "/subscriptions/" + sub + "/providers/Microsoft.Authorization/roleAssignments/ra", isClass5},
		{"DELETE", "/providers/Microsoft.Management/managementGroups/mg", isClass5},
		{"PUT", rg + "/providers/Microsoft.Authorization/roleAssignments/ra", isClass5},
		{"PUT", compute + "/providers/Microsoft.Authorization/roleAssignments/ra", isClass5},
		{"PUT", rg + "/providers/Microsoft.Authorization/locks/l", isClass5},
		{"DELETE", aks, isClass5},
		{"POST", compute + "/runCommand", isClass5},
		{"PUT", compute + "/runCommands/rc", isClass5},
		// Row 8: class 4.
		{"PUT", rg, isClass4},
		{"PUT", compute, isClass4},
		{"PATCH", compute, isClass4},
		{"POST", compute + "/start", isClass4},
		{"DELETE", compute, isClass4},
		{"POST", rg + "/exportTemplate", isClass4},
		{"POST", "/subscriptions/" + sub + "/providers/Microsoft.Compute/register", isClass4},
		{"PUT", compute + "/extensions/script", isClass4},
	} {
		for _, target := range []string{c.target, upper(c.target), lower(c.target)} {
			assert.Equal(t, c.want, readingOf(t, c.method, target), c.method+" "+target)
		}
	}
}

// upper and lower change the case of a target's path, which Resource
// Manager folds, and keep its query's.
func upper(target string) string {
	p, q, found := strings.Cut(target, "?")
	if found {
		return strings.ToUpper(p) + "?" + q
	}
	return strings.ToUpper(p)
}

func lower(target string) string {
	p, q, found := strings.Cut(target, "?")
	if found {
		return strings.ToLower(p) + "?" + q
	}
	return strings.ToLower(p)
}
