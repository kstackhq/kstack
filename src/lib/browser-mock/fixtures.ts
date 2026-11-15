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

// Sample data for the browser mock. Records are partial: the mock fills every
// field left out with a type-appropriate default, so add only what a screen shows.

const NOW = Date.now();
const ago = (minutes: number) => new Date(NOW - minutes * 60_000).toISOString();

function condition(type: string, status: 'True' | 'False' | 'Unknown', reason: string, message = '') {
  return {
    type,
    status,
    reason,
    message,
    liveness: true,
    unconfirmed: false,
    transitionedAt: ago(90),
    updatedAt: ago(1),
  };
}

type ClusterInput = {
  id: string;
  context: string;
  server: string;
  uid: string;
  isDefault?: boolean;
  connected: boolean;
};

function cluster({ id, context, server, uid, isDefault = false, connected }: ClusterInput) {
  return {
    id,
    createdAt: ago(60 * 24 * 30),
    updatedAt: ago(5),
    deletionRequestedAt: null,
    spec: {
      name: context,
      enabled: true,
      syncEnabled: true,
      monitoringEnabled: true,
      source: { kubeconfig: { context } },
    },
    status: {
      source: {
        kubeconfig: {
          cluster: { name: context, entry: { server, insecureSkipTLSVerify: false } },
          user: { name: `${context}-admin` },
          isPresent: true,
          isDefault,
        },
      },
      server: { uid: connected ? uid : null, version: 'v1.31.2', endpoint: server },
      principal: { username: `${context}-admin`, groups: ['system:masters', 'system:authenticated'] },
    },
    conditions: connected
      ? [condition('Connected', 'True', 'Connected'), condition('Identified', 'True', 'Identified')]
      : [
          condition('Connected', 'False', 'Unreachable', `dial tcp: lookup ${new URL(server).host}: no such host`),
          condition('Identified', 'False', 'NoConnection'),
        ],
  };
}

export const CLUSTERS = [
  cluster({
    id: 'c-prod',
    context: 'prod-us-east-1',
    server: 'https://api.prod-us-east-1.example.com',
    uid: 'uid-prod',
    isDefault: true,
    connected: true,
  }),
  cluster({
    id: 'c-staging',
    context: 'staging-eu-west-1',
    server: 'https://api.staging-eu-west-1.example.com',
    uid: 'uid-staging',
    connected: true,
  }),
  cluster({
    id: 'c-dev',
    context: 'kind-dev',
    server: 'https://kind-dev.internal:6443',
    uid: 'uid-dev',
    connected: false,
  }),
];

export const CACHES = [
  { id: '101', clusterID: 'c-prod', generation: 1, createdAt: ago(60 * 24 * 30), spec: { serverUid: 'uid-prod' } },
  {
    id: '102',
    clusterID: 'c-staging',
    generation: 1,
    createdAt: ago(60 * 24 * 12),
    spec: { serverUid: 'uid-staging' },
  },
].map((c) => ({ ...c, conditions: [condition('Synced', 'True', 'Syncing')] }));

export const CACHE_HEALTH = CACHES.map((c) => ({
  cacheID: c.id,
  status: 'True',
  reason: 'Watching',
  unhealthyKindRefs: [],
  failure: '',
  totalKinds: 24,
  unhealthyKinds: 0,
  pausedKinds: 0,
  lastUpdateAt: ago(0.2),
  lastLiveAt: ago(0.5),
}));

// --- Kubernetes objects -----------------------------------------------------

let uidSeq = 0;
const uid = () => `00000000-0000-4000-8000-${String(++uidSeq).padStart(12, '0')}`;

function meta(name: string, namespace: string, ageMinutes: number, labels: Record<string, string> = {}) {
  return {
    name,
    namespace,
    uid: uid(),
    creationTimestamp: ago(ageMinutes),
    labels,
    resourceVersion: String(1000 + uidSeq),
  };
}

const NODES = ['ip-10-0-1-21', 'ip-10-0-2-37', 'ip-10-0-3-54'].map((name, i) => ({
  apiVersion: 'v1',
  kind: 'Node',
  metadata: {
    ...meta(`${name}.ec2.internal`, '', 60 * 24 * (40 - i * 7)),
    labels: { 'kubernetes.io/os': 'linux', 'node.kubernetes.io/instance-type': 'm6i.xlarge' },
  },
  spec: { podCIDR: `10.244.${i}.0/24` },
  status: {
    capacity: { cpu: '4', memory: '16Gi', pods: '110' },
    allocatable: { cpu: '3920m', memory: '15Gi', pods: '110' },
    conditions: [{ type: 'Ready', status: 'True', reason: 'KubeletReady', lastTransitionTime: ago(60 * 24) }],
    nodeInfo: { kubeletVersion: 'v1.31.2', osImage: 'Bottlerocket OS 1.24.0', architecture: 'amd64' },
    addresses: [{ type: 'InternalIP', address: `10.0.${i + 1}.${21 + i * 16}` }],
  },
}));

const NAMESPACES = ['default', 'kube-system', 'payments', 'web', 'monitoring'].map((name, i) => ({
  apiVersion: 'v1',
  kind: 'Namespace',
  metadata: meta(name, '', 60 * 24 * (40 - i)),
  status: { phase: 'Active' },
}));

type AppSpec = { name: string; namespace: string; image: string; replicas: number; port: number; crashing?: number };

const APPS: AppSpec[] = [
  { name: 'checkout-api', namespace: 'payments', image: 'ghcr.io/acme/checkout-api:2.14.1', replicas: 3, port: 8080 },
  { name: 'ledger', namespace: 'payments', image: 'ghcr.io/acme/ledger:1.9.0', replicas: 2, port: 9090, crashing: 1 },
  { name: 'storefront', namespace: 'web', image: 'ghcr.io/acme/storefront:5.2.0', replicas: 3, port: 3000 },
  { name: 'image-resizer', namespace: 'web', image: 'ghcr.io/acme/image-resizer:0.8.3', replicas: 1, port: 8000 },
  {
    name: 'coredns',
    namespace: 'kube-system',
    image: 'registry.k8s.io/coredns/coredns:v1.11.3',
    replicas: 2,
    port: 53,
  },
  { name: 'grafana', namespace: 'monitoring', image: 'grafana/grafana:11.3.0', replicas: 1, port: 3000 },
];

// A pod-name-like suffix, stable across reloads.
const hash = (s: string) =>
  ([...s].reduce((h, c) => Math.imul(h ^ c.charCodeAt(0), 2654435761) >>> 0, 7) % 36 ** 5)
    .toString(36)
    .padStart(5, 'x');

const DEPLOYMENTS = APPS.map((a, i) => {
  const ready = a.replicas - (a.crashing ?? 0);
  return {
    apiVersion: 'apps/v1',
    kind: 'Deployment',
    metadata: meta(a.name, a.namespace, 60 * 24 * (12 - i), { app: a.name }),
    spec: {
      replicas: a.replicas,
      selector: { matchLabels: { app: a.name } },
      template: { spec: { containers: [{ name: a.name, image: a.image, ports: [{ containerPort: a.port }] }] } },
    },
    status: {
      replicas: a.replicas,
      readyReplicas: ready,
      updatedReplicas: a.replicas,
      availableReplicas: ready,
      conditions: [{ type: 'Available', status: ready === a.replicas ? 'True' : 'False' }],
    },
  };
});

const PODS = APPS.flatMap((a, ai) =>
  Array.from({ length: a.replicas }, (_, r) => {
    const crashing = r < (a.crashing ?? 0);
    const node = NODES[(ai + r) % NODES.length];
    const name = `${a.name}-${hash(a.name)}f8c-${hash(`${a.name}${r}`)}`;
    const startedAt = ago(60 * (3 + ai * 5 + r));
    return {
      apiVersion: 'v1',
      kind: 'Pod',
      metadata: {
        ...meta(name, a.namespace, 60 * (3 + ai * 5 + r), { app: a.name }),
        ownerReferences: [{ apiVersion: 'apps/v1', kind: 'ReplicaSet', name: `${a.name}-${hash(a.name)}f8c` }],
      },
      spec: {
        nodeName: node.metadata.name,
        containers: [{ name: a.name, image: a.image, ports: [{ containerPort: a.port }] }],
      },
      status: {
        phase: 'Running',
        podIP: `10.244.${(ai + r) % 3}.${10 + ai * 4 + r}`,
        hostIP: node.status.addresses[0].address,
        startTime: startedAt,
        conditions: [{ type: 'Ready', status: crashing ? 'False' : 'True' }],
        containerStatuses: [
          crashing
            ? {
                name: a.name,
                image: a.image,
                ready: false,
                restartCount: 17,
                state: {
                  waiting: { reason: 'CrashLoopBackOff', message: 'back-off 5m0s restarting failed container' },
                },
                lastState: { terminated: { exitCode: 1, reason: 'Error', finishedAt: ago(2) } },
              }
            : {
                name: a.name,
                image: a.image,
                ready: true,
                restartCount: ai === 2 ? 1 : 0,
                state: { running: { startedAt } },
              },
        ],
      },
    };
  }),
);

const SERVICES = APPS.map((a, i) => ({
  apiVersion: 'v1',
  kind: 'Service',
  metadata: meta(a.name, a.namespace, 60 * 24 * (12 - i), { app: a.name }),
  spec: {
    type: a.name === 'storefront' ? 'LoadBalancer' : 'ClusterIP',
    clusterIP: `172.20.${10 + i}.${40 + i * 3}`,
    ports: [
      { name: 'http', port: a.port === 53 ? 53 : 80, targetPort: a.port, protocol: a.port === 53 ? 'UDP' : 'TCP' },
    ],
    selector: { app: a.name },
  },
  status: a.name === 'storefront' ? { loadBalancer: { ingress: [{ hostname: 'a1b2c3.elb.amazonaws.com' }] } } : {},
}));

const CONFIGMAPS = [
  { name: 'kube-root-ca.crt', namespace: 'default' },
  { name: 'checkout-config', namespace: 'payments' },
  { name: 'storefront-flags', namespace: 'web' },
  { name: 'coredns', namespace: 'kube-system' },
].map((c, i) => ({
  apiVersion: 'v1',
  kind: 'ConfigMap',
  metadata: meta(c.name, c.namespace, 60 * 24 * (20 - i)),
  data: { key: 'value' },
}));

const INGRESSES = [
  {
    apiVersion: 'networking.k8s.io/v1',
    kind: 'Ingress',
    metadata: meta('storefront', 'web', 60 * 24 * 9),
    spec: {
      ingressClassName: 'alb',
      rules: [{ host: 'shop.example.com', http: { paths: [{ path: '/', pathType: 'Prefix' }] } }],
    },
    status: { loadBalancer: { ingress: [{ hostname: 'a1b2c3.elb.amazonaws.com' }] } },
  },
];

type KindDef = { apiVersion: string; kind: string; resource: string; scope: string; objects: object[] };

const KIND_DEFS: KindDef[] = [
  { apiVersion: 'v1', kind: 'Node', resource: 'nodes', scope: 'Cluster', objects: NODES },
  { apiVersion: 'v1', kind: 'Namespace', resource: 'namespaces', scope: 'Cluster', objects: NAMESPACES },
  { apiVersion: 'v1', kind: 'Pod', resource: 'pods', scope: 'Namespaced', objects: PODS },
  { apiVersion: 'v1', kind: 'Service', resource: 'services', scope: 'Namespaced', objects: SERVICES },
  { apiVersion: 'v1', kind: 'ConfigMap', resource: 'configmaps', scope: 'Namespaced', objects: CONFIGMAPS },
  { apiVersion: 'v1', kind: 'Secret', resource: 'secrets', scope: 'Namespaced', objects: [] },
  {
    apiVersion: 'v1',
    kind: 'PersistentVolumeClaim',
    resource: 'persistentvolumeclaims',
    scope: 'Namespaced',
    objects: [],
  },
  { apiVersion: 'v1', kind: 'ServiceAccount', resource: 'serviceaccounts', scope: 'Namespaced', objects: [] },
  { apiVersion: 'apps/v1', kind: 'Deployment', resource: 'deployments', scope: 'Namespaced', objects: DEPLOYMENTS },
  { apiVersion: 'apps/v1', kind: 'DaemonSet', resource: 'daemonsets', scope: 'Namespaced', objects: [] },
  { apiVersion: 'apps/v1', kind: 'StatefulSet', resource: 'statefulsets', scope: 'Namespaced', objects: [] },
  { apiVersion: 'apps/v1', kind: 'ReplicaSet', resource: 'replicasets', scope: 'Namespaced', objects: [] },
  { apiVersion: 'batch/v1', kind: 'Job', resource: 'jobs', scope: 'Namespaced', objects: [] },
  { apiVersion: 'batch/v1', kind: 'CronJob', resource: 'cronjobs', scope: 'Namespaced', objects: [] },
  {
    apiVersion: 'networking.k8s.io/v1',
    kind: 'Ingress',
    resource: 'ingresses',
    scope: 'Namespaced',
    objects: INGRESSES,
  },
  {
    apiVersion: 'networking.k8s.io/v1',
    kind: 'NetworkPolicy',
    resource: 'networkpolicies',
    scope: 'Namespaced',
    objects: [],
  },
  { apiVersion: 'rbac.authorization.k8s.io/v1', kind: 'Role', resource: 'roles', scope: 'Namespaced', objects: [] },
  {
    apiVersion: 'rbac.authorization.k8s.io/v1',
    kind: 'RoleBinding',
    resource: 'rolebindings',
    scope: 'Namespaced',
    objects: [],
  },
  {
    apiVersion: 'rbac.authorization.k8s.io/v1',
    kind: 'ClusterRole',
    resource: 'clusterroles',
    scope: 'Cluster',
    objects: [],
  },
  {
    apiVersion: 'rbac.authorization.k8s.io/v1',
    kind: 'ClusterRoleBinding',
    resource: 'clusterrolebindings',
    scope: 'Cluster',
    objects: [],
  },
  { apiVersion: 'cert-manager.io/v1', kind: 'Certificate', resource: 'certificates', scope: 'Namespaced', objects: [] },
];

export const KINDS = KIND_DEFS.map(({ objects, ...k }) => ({
  ...k,
  isCRD: k.apiVersion.includes('cert-manager'),
  printerColumns: [],
  count: objects.length,
}));

// A cached object as the sidecar streams it: identity fields plus the full body.
export function objectsFor(apiVersion: string, resource: string) {
  const def = KIND_DEFS.find((k) => k.apiVersion === apiVersion && k.resource === resource);
  return (def?.objects ?? []).map((o) => {
    const m = (o as { metadata: ReturnType<typeof meta> }).metadata;
    return {
      uid: m.uid,
      apiVersion,
      kind: def!.kind,
      namespace: m.namespace,
      name: m.name,
      creationTimestamp: m.creationTimestamp,
      rawJSON: o,
    };
  });
}

const ledgerPod = PODS.find((p) => p.metadata.name.startsWith('ledger'))!.metadata;

export const KUBE_EVENTS = [
  {
    type: 'Warning',
    reason: 'BackOff',
    message: `Back-off restarting failed container ledger in pod ${ledgerPod.name}`,
    count: 17,
    firstSeen: ago(85),
    lastSeen: ago(2),
    involvedKind: 'Pod',
    involvedNamespace: 'payments',
    involvedName: ledgerPod.name,
  },
  {
    type: 'Normal',
    reason: 'ScalingReplicaSet',
    message: 'Scaled up replica set storefront-7d9f8c to 3',
    count: 1,
    firstSeen: ago(14),
    lastSeen: ago(14),
    involvedKind: 'Deployment',
    involvedNamespace: 'web',
    involvedName: 'storefront',
  },
  {
    type: 'Normal',
    reason: 'Pulled',
    message: 'Container image "grafana/grafana:11.3.0" already present on machine',
    count: 1,
    firstSeen: ago(31),
    lastSeen: ago(31),
    involvedKind: 'Pod',
    involvedNamespace: 'monitoring',
    involvedName: 'grafana-0',
  },
].map((e) => ({ uid: uid(), ...e }));

// --- Chats ------------------------------------------------------------------

export const CHATS = [
  {
    id: 'chat-1',
    title: 'Why is ledger crash-looping?',
    mode: 'Chat',
    clusterID: 'c-prod',
    createdAt: ago(40),
    updatedAt: ago(36),
  },
  {
    id: 'chat-2',
    title: 'Right-size checkout-api requests',
    mode: 'Chat',
    clusterID: 'c-prod',
    createdAt: ago(60 * 26),
    updatedAt: ago(60 * 25),
  },
  {
    id: 'chat-3',
    title: 'Staging ingress TLS',
    mode: 'Chat',
    clusterID: 'c-staging',
    createdAt: ago(60 * 24 * 3),
    updatedAt: ago(60 * 24 * 3),
  },
  {
    id: 'chat-4',
    title: 'Pods pending in web',
    mode: 'Dashboard',
    clusterID: 'c-prod',
    createdAt: ago(60 * 5),
    updatedAt: ago(60 * 5),
  },
].map((c) => ({ awaitingApproval: false, sandboxDisabled: false, networkEnabled: false, ...c }));

const text = (t: string) => [{ type: 'text', text: t }];

export const MESSAGES: Record<string, object[]> = {
  'chat-1': [
    {
      id: 'm-1',
      chatID: 'chat-1',
      seq: 1,
      role: 'User',
      status: 'Complete',
      content: text('ledger in payments keeps restarting. What is going on?'),
      createdAt: ago(40),
      finishedAt: ago(40),
    },
    {
      id: 'm-2',
      chatID: 'chat-1',
      seq: 2,
      role: 'Assistant',
      status: 'Complete',
      model: 'claude-sonnet-5-5',
      effort: 'medium',
      finishReason: 'end_turn',
      content: text(
        [
          `One of ledger's two replicas, \`${ledgerPod.name}\`, is in **CrashLoopBackOff** with 17 restarts.`,
          '',
          'The container exits with code 1 a few seconds after it starts. The last log lines show it cannot reach its database:',
          '',
          '```',
          'FATAL: connect to postgres://ledger-db.payments:5432: connection refused',
          '```',
          '',
          'The other replica is healthy, so the Service still answers. Next steps:',
          '',
          '1. Check that `ledger-db` is running in `payments`.',
          '2. Confirm the `DATABASE_URL` secret points at the right host.',
        ].join('\n'),
      ),
      createdAt: ago(39),
      finishedAt: ago(36),
    },
  ],
};

export const MODELS = [
  {
    provider: { id: 'anthropic', label: 'Anthropic' },
    id: 'claude-sonnet-5-5',
    label: 'Claude Sonnet 5.5',
    efforts: ['low', 'medium', 'high'],
    defaultEffort: 'medium',
  },
  {
    provider: { id: 'anthropic', label: 'Anthropic' },
    id: 'claude-opus-5-5',
    label: 'Claude Opus 5.5',
    efforts: ['low', 'medium', 'high'],
    defaultEffort: 'medium',
  },
];

export const AUTH_STATE = {
  authenticated: true,
  identity: { sub: 'user-1', email: 'sam@example.com', name: 'Sam Rivera' },
};

export const DATA_RATE = {
  intervalSeconds: 2,
  windowSeconds: 120,
  samples: Array.from({ length: 60 }, (_, i) => ({
    at: new Date(NOW - (60 - i) * 2000).toISOString(),
    receivedBytesPerSecond: 18_000 + Math.round(9_000 * Math.sin(i / 5) + 4_000 * Math.sin(i * 1.7)),
    sentBytesPerSecond: 2_400 + Math.round(800 * Math.sin(i / 3)),
  })),
};
