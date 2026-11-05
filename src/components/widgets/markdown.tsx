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

// An answer's text as markdown, rendered to React elements. Nothing here is ever an
// HTML string: the text is the model's, and the model has read cluster data, so a
// tag in it renders as the literal characters. `react-markdown` leaves raw HTML out
// unless a rehype plugin puts it back — none is added.
import { toJsxRuntime } from 'hast-util-to-jsx-runtime';
import { Check, Copy } from 'lucide-react';
import { Fragment, useEffect, useState } from 'react';
import type { ComponentProps } from 'react';
import ReactMarkdown, { type ExtraProps } from 'react-markdown';
import { jsx, jsxs } from 'react/jsx-runtime';
import remarkGfm from 'remark-gfm';

import '@/components/widgets/markdown.css';
import { copyToClipboard } from '@/lib/clipboard';
import { highlight, load, loaded } from '@/lib/highlight';

type MarkdownProps = {
  text: string;
};

const remarkPlugins = [remarkGfm];

// How long a fence's copy button shows the check after a click.
const COPIED_FOR_MS = 1_500;

// A link is drawn but never followed: the webview has no window to open a page in,
// and the only URLs the host opens are ones it names itself.
function Link({ href, children }: ComponentProps<'a'>) {
  return (
    <span className="underline decoration-dotted" title={href}>
      {children}
    </span>
  );
}

// A fence's language, off the class react-markdown puts on its `<code>`.
function languageOf(className?: string): string | undefined {
  return className?.match(/(?:^|\s)language-(\S+)/)?.[1];
}

// Inline code and the code inside a fence share the tag; the fence's `<pre>` is
// what sets it apart, so the block styles ride there. A fence naming a language
// is highlighted once its grammar has arrived, plain until then; one naming none
// is never guessed at.
function Code({ className, children, ...p }: ComponentProps<'code'>) {
  const lang = languageOf(className);
  const [, arrived] = useState(0);
  useEffect(() => {
    if (lang === undefined || loaded(lang)) return undefined;
    let live = true;
    void load(lang).then((ok) => {
      if (live && ok) arrived((n) => n + 1);
    });
    return () => {
      live = false;
    };
  }, [lang]);

  let body = children;
  if (lang !== undefined && typeof children === 'string' && loaded(lang)) {
    // oxlint-disable-next-line typescript/no-unsafe-assignment -- tsgolint fails to resolve `toJsxRuntime`'s `JSX.Element`, which tsc reads
    body = toJsxRuntime(highlight(lang, children), { Fragment, jsx, jsxs });
  }
  return (
    <code className={`rounded bg-muted px-1 py-0.5 font-mono text-xs ${className ?? ''}`} {...p}>
      {body}
    </code>
  );
}

type Element = NonNullable<ExtraProps['node']>;
type Text = Extract<Element['children'][number], { type: 'text' }>;

// A fence's source, read off its hast node. react-markdown always passes the node,
// and every fence is a `<pre>` over a `<code>` over one text node, an empty one too.
function fenceSource(pre: Element): string {
  const code = pre.children[0] as Element;
  return (code.children[0] as Text).value;
}

// A fence, with a button that copies its source. The check stays `COPIED_FOR_MS`
// after the latest click; the count re-arms the timer on every one.
function CodeBlock({ node, children, ...p }: ComponentProps<'pre'> & ExtraProps) {
  const [copies, setCopies] = useState(0);
  useEffect(() => {
    if (copies === 0) return undefined;
    const timer = setTimeout(() => setCopies(0), COPIED_FOR_MS);
    return () => clearTimeout(timer);
  }, [copies]);

  const copy = () => {
    copyToClipboard(fenceSource(node!));
    setCopies((n) => n + 1);
  };
  const copied = copies > 0;

  return (
    <div className="group/code relative my-2 rounded-md bg-muted">
      <button
        type="button"
        aria-label={copied ? 'Copied' : 'Copy code'}
        className="absolute top-2 right-2 rounded border border-border bg-background/90 p-1.5 text-muted-foreground opacity-0 shadow-sm transition-opacity group-hover/code:opacity-100 group-focus-within/code:opacity-100 hover:text-foreground focus-visible:opacity-100 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
        onClick={copy}
      >
        {copied ? <Check aria-hidden="true" className="size-3.5" /> : <Copy aria-hidden="true" className="size-3.5" />}
      </button>
      <pre className="overflow-x-auto py-3 pr-12 pl-3 text-xs [&>code]:bg-transparent [&>code]:p-0" {...p}>
        {children}
      </pre>
    </div>
  );
}

const components = {
  a: Link,
  p: (p: ComponentProps<'p'>) => <p className="my-2 first:mt-0 last:mb-0" {...p} />,
  h1: ({ children, ...p }: ComponentProps<'h1'>) => (
    <h1 className="mt-4 mb-2 text-base font-semibold first:mt-0" {...p}>
      {children}
    </h1>
  ),
  h2: ({ children, ...p }: ComponentProps<'h2'>) => (
    <h2 className="mt-4 mb-2 text-base font-semibold first:mt-0" {...p}>
      {children}
    </h2>
  ),
  h3: ({ children, ...p }: ComponentProps<'h3'>) => (
    <h3 className="mt-3 mb-1 font-semibold first:mt-0" {...p}>
      {children}
    </h3>
  ),
  h4: ({ children, ...p }: ComponentProps<'h4'>) => (
    <h4 className="mt-3 mb-1 font-semibold first:mt-0" {...p}>
      {children}
    </h4>
  ),
  h5: ({ children, ...p }: ComponentProps<'h5'>) => (
    <h5 className="mt-3 mb-1 font-semibold first:mt-0" {...p}>
      {children}
    </h5>
  ),
  h6: ({ children, ...p }: ComponentProps<'h6'>) => (
    <h6 className="mt-3 mb-1 font-semibold first:mt-0" {...p}>
      {children}
    </h6>
  ),
  ul: (p: ComponentProps<'ul'>) => <ul className="my-2 list-disc pl-5" {...p} />,
  ol: (p: ComponentProps<'ol'>) => <ol className="my-2 list-decimal pl-5" {...p} />,
  li: (p: ComponentProps<'li'>) => <li className="my-0.5" {...p} />,
  blockquote: (p: ComponentProps<'blockquote'>) => (
    <blockquote className="my-2 border-l-2 border-border pl-3 text-muted-foreground" {...p} />
  ),
  hr: (p: ComponentProps<'hr'>) => <hr className="my-3 border-border" {...p} />,
  code: Code,
  pre: CodeBlock,
  table: (p: ComponentProps<'table'>) => (
    <div className="my-2 overflow-x-auto">
      <table className="w-full border-collapse text-xs" {...p} />
    </div>
  ),
  th: (p: ComponentProps<'th'>) => <th className="border border-border px-2 py-1 text-left font-semibold" {...p} />,
  td: (p: ComponentProps<'td'>) => <td className="border border-border px-2 py-1" {...p} />,
};

export function Markdown({ text }: MarkdownProps) {
  return (
    <ReactMarkdown remarkPlugins={remarkPlugins} components={components}>
      {text}
    </ReactMarkdown>
  );
}
