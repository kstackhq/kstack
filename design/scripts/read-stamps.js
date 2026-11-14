// Returns every local component's and every screen's node id, name, stamp and
// hash, as `designsync` reads them. Run it through the Figma MCP's use_figma
// on the Desktop file, save what it returns to a file unchanged, and pass
// that file's path.
// use_figma runs this as a function body, so it awaits and returns at the top.

const componentsPage = figma.root.children.find((p) => p.name === 'Components');
const screensPage = figma.root.children.find((p) => p.name === 'Screens');
// Loads both pages without making either current.
await componentsPage.loadAsync();
await screensPage.loadAsync();

// What the hash covers: what a layer looks like and how it lays out. A
// property left out, such as the description, can change without the
// component reading as edited.
const HASHED = [
  'type', 'name', 'visible', 'opacity', 'blendMode', 'isMask', 'rotation', 'width', 'height',
  'layoutMode', 'layoutWrap', 'primaryAxisSizingMode', 'counterAxisSizingMode', 'primaryAxisAlignItems',
  'counterAxisAlignItems', 'itemSpacing', 'counterAxisSpacing', 'paddingLeft', 'paddingRight', 'paddingTop',
  'paddingBottom', 'layoutSizingHorizontal', 'layoutSizingVertical', 'layoutPositioning', 'constraints', 'clipsContent',
  'fills', 'strokes', 'strokeWeight', 'strokeTopWeight', 'strokeBottomWeight', 'strokeLeftWeight', 'strokeRightWeight',
  'strokeAlign', 'strokeCap', 'strokeJoin', 'dashPattern', 'effects', 'cornerRadius', 'topLeftRadius', 'topRightRadius',
  'bottomLeftRadius', 'bottomRightRadius', 'fillStyleId', 'strokeStyleId', 'effectStyleId', 'textStyleId', 'boundVariables',
  'characters', 'fontName', 'fontSize', 'lineHeight', 'letterSpacing', 'textAlignHorizontal', 'textAlignVertical',
  'textAutoResize', 'textCase', 'textDecoration', 'textTruncation', 'maxLines', 'vectorPaths',
  'componentProperties', 'componentPropertyDefinitions', 'explicitVariableModes',
];

// What a screen's hash covers of an instance: which component it is, where it
// sits and how the screen sets it. What the component draws is its own hash's.
const HASHED_INSTANCE = [
  'type', 'name', 'visible', 'opacity', 'rotation', 'width', 'height', 'layoutSizingHorizontal', 'layoutSizingVertical',
  'layoutPositioning', 'constraints', 'componentProperties', 'explicitVariableModes',
];

function props(n, names) {
  const out = {};
  for (const p of names) {
    if (!(p in n)) continue;
    try {
      out[p] = n[p];
    } catch {
      // The getter throws on this node: a variant has no property definitions.
    }
  }
  return out;
}

const mainKey = async (n) => (await n.getMainComponentAsync())?.key ?? null;

// Every value a screen overrides inside an instance, by layer.
async function overrides(instance) {
  const out = [];
  for (const { id, overriddenFields } of instance.overrides) {
    const layer = await figma.getNodeByIdAsync(id);
    const o = { id, ...props(layer, overriddenFields) };
    // A swapped instance: its fields name the swap, not the component.
    if (layer.type === 'INSTANCE') o.main = await mainKey(layer);
    out.push(o);
  }
  return out.sort((a, b) => (a.id < b.id ? -1 : 1));
}

const STAMP = /\{\s*"kstack"[\s\S]*\}/;

// A screen's stamp is a text layer, since a section has no description.
const isStamp = (n) => n.type === 'TEXT' && n.name === 'Stamp';

// A component's snapshot holds every layer, its instances' included. A
// screen's stops at each instance, and leaves out its stamp and the sections
// nested in it, which are screens of their own.
async function snapshot(n, isRoot, screen) {
  if (screen && n.type === 'INSTANCE') {
    return { ...props(n, HASHED_INSTANCE), x: n.x, y: n.y, main: await mainKey(n), overrides: await overrides(n) };
  }
  const out = props(n, HASHED);
  // The node's own place on the canvas is not part of its design.
  if (!isRoot) {
    out.x = n.x;
    out.y = n.y;
  }
  if (n.type === 'INSTANCE') out.main = await mainKey(n);
  if ('children' in n) {
    out.children = [];
    for (const child of n.children) {
      if (screen && (child.type === 'SECTION' || isStamp(child))) continue;
      out.children.push(await snapshot(child, false, screen));
    }
  }
  return out;
}

// figma.mixed is a symbol, which JSON drops. Rounding keeps float noise out.
const plain = (key, v) => (typeof v === 'symbol' ? 'mixed' : typeof v === 'number' ? Math.round(v * 100) / 100 : v);

// 64-bit FNV-1a: the plugin sandbox has no crypto, and the hash only has to
// tell an edit from none.
function hash(s) {
  let h = 0xcbf29ce484222325n;
  for (let i = 0; i < s.length; i++) {
    h ^= BigInt(s.charCodeAt(i));
    h = (h * 0x100000001b3n) & 0xffffffffffffffffn;
  }
  return h.toString(16).padStart(16, '0');
}

// The stamp runs from its opening brace to the text's last one, however a
// person spaced it.
async function read(n, text, screen) {
  const out = { node: n.id, name: n.name, hash: hash(JSON.stringify(await snapshot(n, true, screen), plain)), stamp: null };
  const stamp = text.match(STAMP);
  if (!stamp) return out;
  try {
    out.stamp = JSON.parse(stamp[0]);
  } catch {
    out.invalid = true;
  }
  return out;
}

const components = [];
for (const n of componentsPage.findAllWithCriteria({ types: ['COMPONENT', 'COMPONENT_SET'] })) {
  // A variant is its component set's, which carries the description.
  if (n.parent.type === 'COMPONENT_SET') continue;
  // descriptionMarkdown: description holds `<` and `'` as HTML entities.
  components.push(await read(n, n.descriptionMarkdown, false));
}

// A screen is a section that holds something drawn, not only other sections.
const screens = [];
for (const n of screensPage.findAllWithCriteria({ types: ['SECTION'] })) {
  if (!n.children.some((c) => c.type !== 'SECTION' && !isStamp(c))) continue;
  screens.push(await read(n, n.children.find(isStamp)?.characters ?? '', true));
}

// The file reaches designsync through the agent that saves it. The sum lets
// designsync refuse one that was mistyped on the way: checksum in
// tools/designsync/stamps.go joins the same fields.
const fields = [...components, ...screens].flatMap((c) => [
  c.node,
  c.name,
  c.hash,
  ...(c.stamp ? [c.stamp.id, ...(c.stamp.changes || [])] : ['']),
]);
return { components, screens, sum: hash(fields.join('\n')) };
