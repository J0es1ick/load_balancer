export function patchMarkup(root: HTMLElement, markup: string): void {
  const template = document.createElement('template')
  template.innerHTML = markup
  patchChildren(root, template.content)
}

function key(node: Node): string | null {
  if (!(node instanceof Element)) return null
  for (const attribute of ['id', 'data-render-key', 'data-endpoint-key', 'data-section', 'data-cluster-strategy', 'data-endpoint-count']) {
    if (node.hasAttribute(attribute)) return `${attribute}:${node.getAttribute(attribute)}`
  }
  if (node.hasAttribute('data-action')) {
    return `action:${node.getAttribute('data-action')}:${node.getAttribute('data-cluster') ?? ''}:${node.getAttribute('data-endpoint') ?? ''}`
  }
  return null
}

function compatible(current: Node, next: Node): boolean {
  if (current.nodeType !== next.nodeType || key(current) !== key(next)) return false
  return !(current instanceof Element && next instanceof Element)
    || (current.localName === next.localName && current.namespaceURI === next.namespaceURI)
}

function patchChildren(current: Node, next: Node): void {
  const previous: Node[] = Array.from(current.childNodes)
  const unused = new Set(previous)
  const keyed = new Map(previous.flatMap((node) => key(node) === null ? [] : [[key(node), node] as const]))
  let cursor: Node | null = current.firstChild
  for (const desired of Array.from(next.childNodes)) {
    const identity = key(desired)
    let mounted = identity === null ? cursor : keyed.get(identity) ?? null
    if (!mounted || !unused.has(mounted) || !compatible(mounted, desired)) {
      mounted = identity === null
        ? previous.find((node) => unused.has(node) && compatible(node, desired)) ?? null
        : null
    }
    if (mounted) {
      unused.delete(mounted)
      if (mounted !== cursor) current.insertBefore(mounted, cursor)
      patchNode(mounted, desired)
    } else {
      mounted = desired.cloneNode(true)
      current.insertBefore(mounted, cursor)
    }
    cursor = mounted.nextSibling
  }
  for (const node of unused) current.removeChild(node)
}

function patchNode(current: Node, next: Node): void {
  if (!(current instanceof Element) || !(next instanceof Element)) {
    if (current.nodeValue !== next.nodeValue) current.nodeValue = next.nodeValue
    return
  }
  if (current.hasAttribute('data-dom-owned')) return
  const focused = current === current.ownerDocument.activeElement
  const browserOwned = (name: string) => (name === 'open' && current instanceof HTMLDetailsElement)
    || (focused && (name === 'value' || name === 'checked' || name === 'selected'))
  for (const attribute of Array.from(current.attributes)) {
    if (!browserOwned(attribute.name) && !next.hasAttribute(attribute.name)) current.removeAttribute(attribute.name)
  }
  for (const attribute of Array.from(next.attributes)) {
    if (!browserOwned(attribute.name) && current.getAttribute(attribute.name) !== attribute.value) {
      current.setAttribute(attribute.name, attribute.value)
    }
  }
  if (current instanceof HTMLTextAreaElement && next instanceof HTMLTextAreaElement) {
    if (!focused && current.value !== next.value) current.value = next.value
    return
  }
  if (current instanceof HTMLInputElement && next instanceof HTMLInputElement) {
    if (!focused && current.value !== next.value) current.value = next.value
    if (!focused && current.checked !== next.checked) current.checked = next.checked
    return
  }
  if (current instanceof HTMLSelectElement && focused) return
  patchChildren(current, next)
  if (current instanceof HTMLSelectElement && next instanceof HTMLSelectElement && current.value !== next.value) {
    current.value = next.value
  }
}
