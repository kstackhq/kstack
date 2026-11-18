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

// The services Kstack signs in to, whose credentials providers and tools use.

export type Connector = {
  id: string;
  name: string;
  // The second line: the account, profile or what the service offers.
  detail: string;
  connected: boolean;
  // The label of the item that opens the service's own settings, if it has one.
  manageLabel?: string;
};

// The connectors Kstack plans to ship. Placeholder: none is connected until a
// connector backend exists.
const CONNECTORS: Connector[] = [
  { id: 'github', name: 'GitHub', detail: 'Code resources and Copilot models', connected: false },
  { id: 'aws', name: 'AWS', detail: 'Cloud resources and Bedrock models', connected: false },
  { id: 'google', name: 'Google', detail: 'Cloud resources and Gemini models', connected: false },
  { id: 'azure', name: 'Azure', detail: 'Cloud resources and Foundry models', connected: false },
  { id: 'chatgpt', name: 'ChatGPT', detail: 'OpenAI models on your ChatGPT plan', connected: false },
  { id: 'openrouter', name: 'OpenRouter', detail: 'Many providers on one account', connected: false },
];

export function useConnectors(): Connector[] {
  return CONNECTORS;
}
