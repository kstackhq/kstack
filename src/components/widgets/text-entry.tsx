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

import type { ComponentProps } from 'react';

// eslint-disable-next-line no-restricted-imports -- the one place the library's boxes are drawn
import { Input as UiInput } from '@kubetail/ui/elements/input';
// eslint-disable-next-line no-restricted-imports -- the one place the library's boxes are drawn
import { Textarea as UiTextarea } from '@kubetail/ui/elements/textarea';

// Left alone, the webview suggests, corrects and capitalizes as the user types
// (WKWebView inline predictions most of all). That fights code-like text: a
// grep pattern, a path, a resource name. `writingsuggestions` is WebKit's
// switch for inline predictions; React passes it through as written.
export const CODE_TEXT = {
  autoComplete: 'off',
  autoCorrect: 'off',
  autoCapitalize: 'off',
  spellCheck: false,
  writingsuggestions: 'false',
} as const;

// Prose keeps the spellcheck underline, which changes nothing unasked.
export const PROSE_TEXT = { ...CODE_TEXT, spellCheck: true } as const;

type TextProps = { prose?: boolean };

export function Input({ prose = false, ...props }: ComponentProps<'input'> & TextProps) {
  return <UiInput {...(prose ? PROSE_TEXT : CODE_TEXT)} {...props} />;
}

export function Textarea({ prose = false, ...props }: ComponentProps<'textarea'> & TextProps) {
  return <UiTextarea {...(prose ? PROSE_TEXT : CODE_TEXT)} {...props} />;
}
